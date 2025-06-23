// Copyright (c) 2014 Derek Parker
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// This file may have been modified by CloudWeGo authors. All CloudWeGo
// Modifications are Copyright 2025 CloudWeGo Authors.

package proc

import (
	"errors"
	"fmt"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
)

const (
	// hashTophashEmptyZero is used by map reading code, indicates an empty cell
	hashTophashEmptyZero = 0 // +rtype emptyRest
	// hashTophashEmptyOne is used by map reading code, indicates an empty cell in Go 1.12 and later
	hashTophashEmptyOne = 1 // +rtype emptyOne
	// hashMinTopHashGo111 used by map reading code, indicates minimum value of tophash that isn't empty or evacuated, in Go1.11
	hashMinTopHashGo111 = 4 // +rtype minTopHash
	// hashMinTopHashGo112 is used by map reading code, indicates minimum value of tophash that isn't empty or evacuated, in Go1.12
	hashMinTopHashGo112 = 5 // +rtype minTopHash
)

// The kind field in runtime._type is a reflect.Kind value plus
// some extra flags defined here.
// See equivalent declaration in $GOROOT/src/reflect/type.go
const (
	kindDirectIface = 1 << 5 // +rtype kindDirectIface|internal/abi.KindDirectIface
	kindGCProg      = 1 << 6 // +rtype kindGCProg|internal/abi.KindGCProg
	kindNoPointers  = 1 << 7
	kindMask        = (1 << 5) - 1 // +rtype kindMask|internal/abi.KindMask
)

type mapIterator interface {
	next(s *ObjRefScope) bool
	key() *ReferenceVariable
	value() *ReferenceVariable
	referenceInfo() (objects []*ReferenceVariable, size, count int64)
}

// Code derived from go/src/runtime/hashmap.go
func (s *ObjRefScope) toMapIterator(hmap *ReferenceVariable, keyType, elemType godwarf.Type) (mi mapIterator, err error) {
	if hmap.Addr == 0 {
		err = errors.New("empty hmap addr")
		return
	}
	maptype, ok := hmap.RealType.(*godwarf.StructType)
	if !ok {
		err = errors.New("wrong real type for map")
		return
	}

	isptr := func(typ godwarf.Type) bool {
		_, isptr := typ.(*godwarf.PtrType)
		return isptr
	}

	it := &mapIteratorClassic{bidx: 0, b: nil, idx: 0, keyTypeIsPtr: isptr(keyType), elemTypeIsPtr: isptr(elemType), bi: s.bi, size: hmap.size, count: hmap.count}
	itswiss := &mapIteratorSwiss{keyTypeIsPtr: isptr(keyType), elemTypeIsPtr: isptr(elemType), bi: s.bi, size: hmap.size, count: hmap.count}

	for _, f := range maptype.Field {
		switch f.Name {
		// case "count": // +rtype -fieldof hmap int
		//	v.Len, err = readIntRaw(mem, uint64(addr.Add(f.ByteOffset)), ptrSize)
		case "B": // +rtype -fieldof hmap uint8
			mi = it
			var b uint64
			b, err = readUintRaw(hmap.mem, uint64(hmap.Addr.Add(f.ByteOffset)), 1)
			if err != nil {
				return
			}
			it.numbuckets = 1 << b
			it.oldmask = (1 << (b - 1)) - 1
		case "buckets": // +rtype -fieldof hmap unsafe.Pointer
			mi = it
			field := hmap.toField(f)
			buckets := field.dereference(s)
			if buckets != nil {
				it.buckets = buckets
				it.size += buckets.size
				it.count += buckets.count
				it.objects = append(it.objects, buckets)
			}
		case "oldbuckets": // +rtype -fieldof hmap unsafe.Pointer
			mi = it
			field := hmap.toField(f)
			oldbuckets := field.dereference(s)
			if oldbuckets != nil {
				it.oldbuckets = oldbuckets
				it.size += oldbuckets.size
				it.count += oldbuckets.count
				it.objects = append(it.objects, oldbuckets)
			}

		// swisstable map fields
		case "dirPtr":
			mi = itswiss
			itswiss.dirPtr = newReferenceVariable(hmap.Addr.Add(f.ByteOffset), "", f.Type, hmap.mem, hmap.hb)
		case "dirLen":
			mi = itswiss
			itswiss.dirLen, err = readIntRaw(hmap.mem, uint64(hmap.Addr.Add(f.ByteOffset)), 8)
			if err != nil {
				return
			}
		}
	}

	if it.buckets == nil && itswiss.dirPtr != nil {
		err = itswiss.loadTypes(s)
		if err != nil {
			return
		}
		return itswiss, nil
	}

	if it.buckets != nil {
		if _, ok = it.buckets.RealType.(*godwarf.StructType); !ok {
			err = errMapBucketsNotStruct
			return
		}
	}
	if it.oldbuckets != nil {
		if _, ok = it.oldbuckets.RealType.(*godwarf.StructType); !ok {
			err = errMapBucketsNotStruct
			return
		}
	}

	it.hashTophashEmptyOne = hashTophashEmptyZero
	it.hashMinTopHash = hashMinTopHashGo111
	if producer := s.bi.Producer(); producer != "" && goversion.ProducerAfterOrEqual(producer, 1, 12) {
		it.hashTophashEmptyOne = hashTophashEmptyOne
		it.hashMinTopHash = hashMinTopHashGo112
	}
	return it, nil
}

type mapIteratorClassic struct {
	bi         *proc.BinaryInfo
	numbuckets uint64
	oldmask    uint64
	buckets    *ReferenceVariable
	oldbuckets *ReferenceVariable
	b          *ReferenceVariable
	bidx       uint64

	keyTypeIsPtr, elemTypeIsPtr bool

	tophashes *ReferenceVariable
	keys      *ReferenceVariable
	values    *ReferenceVariable
	overflow  *ReferenceVariable

	maxNumBuckets uint64 // maximum number of buckets to scan

	idx int64

	hashTophashEmptyOne uint64 // Go 1.12 and later has two sentinel tophash values for an empty cell, this is the second one (the first one hashTophashEmptyZero, the same as Go 1.11 and earlier)
	hashMinTopHash      uint64 // minimum value of tophash for a cell that isn't either evacuated or empty

	// for record ref mem
	objects     []*ReferenceVariable
	size, count int64
}

var (
	errMapBucketContentsNotArray        = errors.New("malformed map type: keys, values or tophash of a bucket is not an array")
	errMapBucketContentsInconsistentLen = errors.New("malformed map type: inconsistent array length in bucket")
	errMapBucketsNotStruct              = errors.New("malformed map type: buckets, oldbuckets or overflow field not a struct")
)

func (it *mapIteratorClassic) nextBucket(s *ObjRefScope) bool {
	if it.overflow != nil && it.overflow.Addr > 0 {
		it.b = it.overflow
	} else {
		it.b = nil

		if it.maxNumBuckets > 0 && it.bidx >= it.maxNumBuckets {
			return false
		}

		for it.bidx < it.numbuckets {
			if it.buckets == nil {
				break
			}
			it.b = it.buckets.clone()
			it.b.Addr = it.b.Addr.Add(it.buckets.RealType.Size() * int64(it.bidx))

			if it.oldbuckets == nil {
				break
			}

			// if oldbuckets is not nil we are iterating through a map that is in
			// the middle of a grow.
			// if the bucket we are looking at hasn't been filled in we iterate
			// instead through its corresponding "oldbucket" (i.e. the bucket the
			// elements of this bucket are coming from) but only if this is the first
			// of the two buckets being created from the same oldbucket (otherwise we
			// would print some keys twice)

			oldbidx := it.bidx & it.oldmask
			oldb := it.oldbuckets.clone()
			oldb.Addr = oldb.Addr.Add(it.oldbuckets.RealType.Size() * int64(oldbidx))

			if it.mapEvacuated(oldb) {
				break
			}

			if oldbidx == it.bidx {
				it.b = oldb
				break
			}

			// oldbucket origin for current bucket has not been evacuated but we have already
			// iterated over it so we should just skip it
			it.b = nil
			it.bidx++
		}

		if it.b == nil {
			return false
		}
		it.bidx++
	}

	if it.b.Addr <= 0 {
		return false
	}

	it.tophashes = nil
	it.keys = nil
	it.values = nil
	it.overflow = nil

	for _, f := range it.b.RealType.(*godwarf.StructType).Field {
		switch f.Name {
		case "tophash": // +rtype -fieldof bmap [8]uint8
			it.tophashes = it.b.toField(f)
		case "keys":
			it.keys = it.b.toField(f)
		case "values":
			it.values = it.b.toField(f)
		case "overflow":
			field := it.b.toField(f)
			if it.overflow = field.dereference(s); it.overflow != nil {
				it.count += it.overflow.count
				it.size += it.overflow.size
				it.objects = append(it.objects, it.overflow)
			}
		}
	}

	// sanity checks
	if it.tophashes == nil || it.keys == nil || it.values == nil {
		logflags.DebuggerLogger().Errorf("malformed map type")
		return false
	}

	tophashesType, ok1 := it.tophashes.RealType.(*godwarf.ArrayType)
	keysType, ok2 := it.keys.RealType.(*godwarf.ArrayType)
	valuesType, ok3 := it.values.RealType.(*godwarf.ArrayType)
	if !ok1 || !ok2 || !ok3 {
		logflags.DebuggerLogger().Errorf("%v", errMapBucketContentsNotArray)
		return false
	}

	if tophashesType.Count != keysType.Count {
		logflags.DebuggerLogger().Errorf("%v", errMapBucketContentsInconsistentLen)
		return false
	}

	if valuesType.Type.Size() > 0 && tophashesType.Count != valuesType.Count {
		// if the type of the value is zero-sized (i.e. struct{}) then the values
		// array's length is zero.
		logflags.DebuggerLogger().Errorf("%v", errMapBucketContentsInconsistentLen)
		return false
	}

	if it.overflow != nil {
		if _, ok := it.overflow.RealType.(*godwarf.StructType); !ok {
			logflags.DebuggerLogger().Errorf("%v", errMapBucketsNotStruct)
			return false
		}
	}

	return true
}

func (it *mapIteratorClassic) next(s *ObjRefScope) bool {
	for {
		if it.b == nil {
			r := it.nextBucket(s)
			if !r {
				return false
			}
			it.idx = 0
		}
		if tophashesType, _ := it.tophashes.RealType.(*godwarf.ArrayType); it.idx >= tophashesType.Count {
			r := it.nextBucket(s)
			if !r {
				return false
			}
			it.idx = 0
		}
		tophash := it.tophashes.clone()
		tophash.RealType = tophash.RealType.(*godwarf.ArrayType).Type
		tophash.Name = fmt.Sprintf("[%d]", int(it.idx))
		tophash.Addr = tophash.Addr.Add(tophash.RealType.Size() * it.idx)

		h, err := readUintRaw(tophash.mem, uint64(tophash.Addr), 1)
		if err != nil {
			logflags.DebuggerLogger().Errorf("unreadable tophash: %v", err)
			return false
		}
		it.idx++
		if h != hashTophashEmptyZero && h != it.hashTophashEmptyOne {
			return true
		}
	}
}

func (it *mapIteratorClassic) key() *ReferenceVariable {
	return it.kv(it.keys.clone())
}

func (it *mapIteratorClassic) value() *ReferenceVariable {
	return it.kv(it.values.clone())
}

func (it *mapIteratorClassic) kv(v *ReferenceVariable) *ReferenceVariable {
	v.RealType = resolveTypedef(v.RealType.(*godwarf.ArrayType).Type)
	v.Addr = v.Addr.Add(v.RealType.Size() * (it.idx - 1))
	return v
}

func (it *mapIteratorClassic) mapEvacuated(b *ReferenceVariable) bool {
	if b.Addr == 0 {
		return true
	}
	for _, f := range b.RealType.(*godwarf.StructType).Field {
		if f.Name != "tophash" {
			continue
		}
		tophash0, err := readUintRaw(b.mem, uint64(b.Addr.Add(f.ByteOffset)), 1)
		if err != nil {
			return true
		}
		// TODO: this needs to be > hashTophashEmptyOne for go >= 1.12
		return tophash0 > it.hashTophashEmptyOne && tophash0 < it.hashMinTopHash
	}
	return true
}

func (it *mapIteratorClassic) referenceInfo() (objects []*ReferenceVariable, size, count int64) {
	return it.objects, it.size, it.count
}

// Swisstable Maps ///////////////////////////////////////////////////////////////

const (
	swissTableCtrlEmpty = 0b10000000 // +rtype go1.24 internal/runtime/maps.ctrlEmpty
)

type mapIteratorSwiss struct {
	bi           *proc.BinaryInfo
	dirPtr       *ReferenceVariable
	dirLen       int64
	maxNumGroups uint64 // Maximum number of groups we will visit

	keyTypeIsPtr, elemTypeIsPtr bool
	tableType, groupType        *godwarf.StructType

	tableFieldIndex, tableFieldGroups, groupsFieldLengthMask, groupsFieldData, groupFieldCtrl, groupFieldSlots, slotFieldKey, slotFieldElem *godwarf.StructField

	dirIdx int64
	tab    *swissTable

	groupIdx uint64
	group    *swissGroup

	slotIdx uint32

	groupCount uint64 // Total count of visited groups except for current table

	curKey, curValue *ReferenceVariable

	// for record ref mem
	objects     []*ReferenceVariable
	size, count int64
}

type swissTable struct {
	index  int64
	groups *ReferenceVariable
}

type swissGroup struct {
	slots *ReferenceVariable
	ctrls []byte
}

var (
	errSwissTableCouldNotLoad  = errors.New("could not load one of the tables")
	errSwissMapBadType         = errors.New("swiss table type does not have some required fields")
	errSwissMapBadTableField   = errors.New("swiss table bad table field")
	errSwissMapBadGroupTypeErr = errors.New("bad swiss map type, group type lacks some required fields")
	errSwissTableNilGroups     = errors.New("bad swiss map, groups pointer is nil")
)

// loadTypes determines the correct type for it.dirPtr:  the linker records
// this type as **table but in reality it is either *[dirLen]*table for
// large maps or *group for small maps, when it.dirLen == 0.
func (it *mapIteratorSwiss) loadTypes(s *ObjRefScope) error {
	tableptrptrtyp, ok := it.dirPtr.RealType.(*godwarf.PtrType)
	if !ok {
		return errSwissMapBadTableField
	}
	tableptrtyp, ok := tableptrptrtyp.Type.(*godwarf.PtrType)
	if !ok {
		return errSwissMapBadTableField
	}
	it.tableType, ok = tableptrtyp.Type.(*godwarf.StructType)
	if !ok {
		return errSwissMapBadTableField
	}
	for _, field := range it.tableType.Field {
		switch field.Name {
		case "index":
			it.tableFieldIndex = field
		case "groups":
			it.tableFieldGroups = field
			groupstyp, ok := field.Type.(*godwarf.StructType)
			if ok {
				for _, field := range groupstyp.Field {
					switch field.Name {
					case "data":
						it.groupsFieldData = field
						typ, ok := field.Type.(*godwarf.PtrType)
						if ok {
							it.groupType, _ = resolveTypedef(typ.Type).(*godwarf.StructType)
						}
					case "lengthMask":
						it.groupsFieldLengthMask = field
					}
				}
			}
		}
	}
	if it.groupType == nil || it.tableFieldIndex == nil || it.tableFieldGroups == nil || it.groupsFieldLengthMask == nil {
		return errSwissMapBadType
	}
	for _, field := range it.groupType.Field {
		switch field.Name {
		case "ctrl":
			it.groupFieldCtrl = field
		case "slots":
			it.groupFieldSlots = field
		}
	}
	if it.groupFieldCtrl == nil || it.groupFieldSlots == nil {
		return errSwissMapBadGroupTypeErr
	}

	slotsType, ok := resolveTypedef(it.groupFieldSlots.Type).(*godwarf.ArrayType)
	if !ok {
		return errSwissMapBadGroupTypeErr
	}
	slotType, ok := slotsType.Type.(*godwarf.StructType)
	if !ok {
		return errSwissMapBadGroupTypeErr
	}
	for _, field := range slotType.Field {
		switch field.Name {
		case "key":
			it.slotFieldKey = field
		case "elem":
			it.slotFieldElem = field
		}
	}
	if it.slotFieldKey == nil || it.slotFieldElem == nil {
		return errSwissMapBadGroupTypeErr
	}

	if it.dirLen <= 0 {
		// small maps, convert it.dirPtr to be of type *group, then dereference it
		it.dirPtr.RealType = pointerTo(fakeArrayType(1, it.groupType), it.bi.Arch)
		it.dirPtr = it.dirPtr.dereference(s)
		if it.dirPtr == nil {
			return errSwissTableCouldNotLoad
		}
		it.size += it.dirPtr.size
		it.count += it.dirPtr.count
		it.objects = append(it.objects, it.dirPtr)
		it.dirLen = 1
		it.tab = &swissTable{groups: it.dirPtr} // so that we don't try to load this later on
		return nil
	}

	// normal map, convert it.dirPtr to be of type *[dirLen]*table, then dereference it
	it.dirPtr.RealType = pointerTo(fakeArrayType(uint64(it.dirLen), tableptrtyp), it.bi.Arch)
	it.dirPtr = it.dirPtr.dereference(s)
	if it.dirPtr == nil {
		return errSwissTableCouldNotLoad
	}
	it.size += it.dirPtr.size
	it.count += it.dirPtr.count
	it.objects = append(it.objects, it.dirPtr)
	return nil
}

// derived from $GOROOT/src/internal/runtime/maps/table.go and $GOROOT/src/runtime/runtime-gdb.py
func (it *mapIteratorSwiss) next(s *ObjRefScope) bool {
	for it.dirIdx < it.dirLen {
		if it.tab == nil {
			err := it.loadCurrentTable(s)
			if err != nil {
				return false
			}
			if it.tab == nil {
				return false
			}
			if it.tab.index != it.dirIdx {
				it.nextTable()
				continue
			}
		}

		var countGroups uint64
		if it.tab.groups != nil {
			countGroups = it.tab.groups.arrayLen()
		}
		for ; it.groupIdx < countGroups; it.nextGroup() {
			if it.maxNumGroups > 0 && it.groupIdx+it.groupCount >= it.maxNumGroups {
				return false
			}
			if it.group == nil {
				err := it.loadCurrentGroup()
				if err != nil {
					return false
				}
				if it.group == nil {
					return false
				}
			}

			countSlots := it.group.slots.RealType.(*godwarf.ArrayType).Count
			for ; it.slotIdx < uint32(countSlots); it.slotIdx++ {
				if it.slotIsEmptyOrDeleted(it.slotIdx) {
					continue
				}

				cur := it.group.slots.arrayAccess(int64(it.slotIdx))
				if cur == nil {
					return false
				}

				it.curKey = cur.toField(it.slotFieldKey)
				it.curValue = cur.toField(it.slotFieldElem)
				if it.curKey == nil || it.curValue == nil {
					return false
				}

				// If the type we expect is non-pointer but we read a pointer type it
				// means that the key (or the value) is stored indirectly into the map
				// because it is too big. We dereference it here so that the type of the
				// key (or value) matches the type on the map definition.
				if _, ok := it.curKey.RealType.(*godwarf.PtrType); ok && !it.keyTypeIsPtr {
					it.curKey = it.curKey.dereference(s)
					if it.curKey != nil {
						it.size += it.curKey.size
						it.count += it.curKey.count
						it.objects = append(it.objects, it.curKey)
					}
				}
				if _, ok := it.curValue.RealType.(*godwarf.PtrType); ok && !it.elemTypeIsPtr {
					it.curValue = it.curValue.dereference(s)
					if it.curValue != nil {
						it.size += it.curValue.size
						it.count += it.curValue.count
						it.objects = append(it.objects, it.curValue)
					}
				}

				it.slotIdx++
				return true
			}

			it.slotIdx = 0
		}

		it.groupCount += it.groupIdx
		it.groupIdx = 0
		it.group = nil
		it.nextTable()
	}
	return false
}

func (it *mapIteratorSwiss) nextTable() {
	it.dirIdx++
	it.tab = nil
}

func (it *mapIteratorSwiss) nextGroup() {
	it.groupIdx++
	it.group = nil
}

// loadCurrentTable loads the table at index it.dirIdx into it.tab
func (it *mapIteratorSwiss) loadCurrentTable(s *ObjRefScope) (err error) {
	tab := it.dirPtr.arrayAccess(it.dirIdx)
	if tab == nil {
		return errSwissTableCouldNotLoad
	}
	tab = tab.dereference(s)
	if tab == nil {
		return errSwissTableCouldNotLoad
	}
	it.size += tab.size
	it.count += tab.count
	it.objects = append(it.objects, tab)

	r := &swissTable{}

	field := tab.toField(it.tableFieldIndex)
	r.index, err = field.asInt()
	if err != nil {
		return fmt.Errorf("could not load swiss table index: %v", err)
	}

	groups := tab.toField(it.tableFieldGroups)
	r.groups = groups.toField(it.groupsFieldData)

	field = groups.toField(it.groupsFieldLengthMask)
	groupsLengthMask, err := field.asUint()
	if err != nil {
		return fmt.Errorf("could not load swiss table group lengthMask: %v", err)
	}

	// convert the type of groups from *group to *[len]group so that it's easier to use
	r.groups.RealType = pointerTo(fakeArrayType(groupsLengthMask+1, it.groupType), it.bi.Arch)
	r.groups = r.groups.dereference(s)
	if r.groups == nil {
		return errSwissTableNilGroups
	}

	it.size += r.groups.size
	it.count += r.groups.count
	it.objects = append(it.objects, r.groups)

	it.tab = r
	return nil
}

// loadCurrentGroup loads the group at index it.groupIdx of it.tab into it.group
func (it *mapIteratorSwiss) loadCurrentGroup() error {
	group := it.tab.groups.arrayAccess(int64(it.groupIdx))
	if group == nil {
		return fmt.Errorf("could not load swiss map group")
	}
	g := &swissGroup{}
	g.slots = group.toField(it.groupFieldSlots)
	ctrl := group.toField(it.groupFieldCtrl)
	g.ctrls = make([]byte, ctrl.RealType.Size())
	_, err := ctrl.mem.ReadMemory(g.ctrls, uint64(ctrl.Addr))
	if err != nil {
		return err
	}
	it.group = g
	return nil
}

func (it *mapIteratorSwiss) key() *ReferenceVariable {
	return it.curKey
}

func (it *mapIteratorSwiss) value() *ReferenceVariable {
	return it.curValue
}

func (it *mapIteratorSwiss) slotIsEmptyOrDeleted(k uint32) bool {
	// TODO: check that this hasn't changed after it's merged and the TODO is deleted
	return it.group.ctrls[k]&swissTableCtrlEmpty == swissTableCtrlEmpty
}

func (it *mapIteratorSwiss) referenceInfo() (objects []*ReferenceVariable, size, count int64) {
	return it.objects, it.size, it.count
}
