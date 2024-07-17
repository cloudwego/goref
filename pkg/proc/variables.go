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
// Modifications are Copyright 2024 CloudWeGo Authors.

package proc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"

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

// ReferenceVariable represents a variable. It contains the address, name,
// type and other information parsed from both the Dwarf information
// and the memory of the debugged process.
type ReferenceVariable struct {
	Addr     Address
	Name     string
	RealType godwarf.Type
	mem      proc.MemoryReadWriter

	// heap bits for this object
	// hb.base equals to Addr, hb.end equals to min(Addr.Add(RealType.Size), heapBase.Add(elemSize))
	hb *heapBits

	// node size
	size int64
	// node count
	count int64
}

func newReferenceVariable(addr Address, name string, typ godwarf.Type, mem proc.MemoryReadWriter, hb *heapBits) *ReferenceVariable {
	return &ReferenceVariable{Addr: addr, Name: name, RealType: typ, mem: mem, hb: hb}
}

func newReferenceVariableWithSizeAndCount(addr Address, name string, typ godwarf.Type, mem proc.MemoryReadWriter, hb *heapBits, size, count int64) *ReferenceVariable {
	rv := newReferenceVariable(addr, name, typ, mem, hb)
	rv.size, rv.count = size, count
	return rv
}

func (v *ReferenceVariable) readPointer(addr Address) (uint64, error) {
	v.hb.resetGCMask(v.Addr)
	return readUintRaw(v.mem, uint64(addr), 8)
}

// To avoid traversing fields/elements that escape the actual valid scope.
// e.g. (*[1 << 16]scase)(unsafe.Pointer(cas0)) in runtime.selectgo.
func (v *ReferenceVariable) isValid(addr Address) bool {
	if v.hb == nil {
		return true
	}
	return addr >= v.hb.base && addr < v.hb.end
}

type mapIterator struct {
	bi         *proc.BinaryInfo
	numbuckets uint64
	oldmask    uint64
	buckets    *ReferenceVariable
	oldbuckets *ReferenceVariable
	b          *ReferenceVariable
	bidx       uint64

	tophashes *ReferenceVariable
	keys      *ReferenceVariable
	values    *ReferenceVariable
	overflow  *ReferenceVariable

	maxNumBuckets uint64 // maximum number of buckets to scan

	idx int64

	hashTophashEmptyOne uint64 // Go 1.12 and later has two sentinel tophash values for an empty cell, this is the second one (the first one hashTophashEmptyZero, the same as Go 1.11 and earlier)
	hashMinTopHash      uint64 // minimum value of tophash for a cell that isn't either evacuated or empty

	// for record ref mem
	size, count int64
}

// Code derived from go/src/runtime/hashmap.go
func (s *ObjRefScope) toMapIterator(hmap *ReferenceVariable) (it *mapIterator, err error) {
	if hmap.Addr == 0 {
		err = errors.New("empty hmap addr")
		return
	}
	maptype, ok := hmap.RealType.(*godwarf.StructType)
	if !ok {
		err = errors.New("wrong real type for map")
		return
	}

	it = &mapIterator{bidx: 0, b: nil, idx: 0, bi: s.bi, size: hmap.size, count: hmap.count}

	for _, f := range maptype.Field {
		switch f.Name {
		// case "count": // +rtype -fieldof hmap int
		//	v.Len, err = readIntRaw(mem, uint64(addr.Add(f.ByteOffset)), ptrSize)
		case "B": // +rtype -fieldof hmap uint8
			var b uint64
			b, err = readUintRaw(hmap.mem, uint64(hmap.Addr.Add(f.ByteOffset)), 1)
			if err != nil {
				return
			}
			it.numbuckets = 1 << b
			it.oldmask = (1 << (b - 1)) - 1
		case "buckets": // +rtype -fieldof hmap unsafe.Pointer
			var ptr uint64
			ptr, err = hmap.readPointer(hmap.Addr.Add(f.ByteOffset))
			if err != nil {
				return
			}
			buckets := s.findObject(Address(ptr), resolveTypedef(f.Type.(*godwarf.PtrType).Type), proc.DereferenceMemory(hmap.mem))
			if buckets != nil {
				buckets.Name = "buckets"
				it.buckets = buckets
				it.size += buckets.size
				it.count += buckets.count
			}
		case "oldbuckets": // +rtype -fieldof hmap unsafe.Pointer
			var ptr uint64
			ptr, err = hmap.readPointer(hmap.Addr.Add(f.ByteOffset))
			if err != nil {
				return
			}
			oldbuckets := s.findObject(Address(ptr), resolveTypedef(f.Type.(*godwarf.PtrType).Type), proc.DereferenceMemory(hmap.mem))
			if oldbuckets != nil {
				oldbuckets.Name = "oldbuckets"
				it.oldbuckets = oldbuckets
				it.size += oldbuckets.size
				it.count += oldbuckets.count
			}
		}
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
	return
}

var (
	errMapBucketContentsNotArray        = errors.New("malformed map type: keys, values or tophash of a bucket is not an array")
	errMapBucketContentsInconsistentLen = errors.New("malformed map type: inconsistent array length in bucket")
	errMapBucketsNotStruct              = errors.New("malformed map type: buckets, oldbuckets or overflow field not a struct")
)

func (s *ObjRefScope) nextBucket(it *mapIterator) bool {
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
		field := newReferenceVariable(it.b.Addr.Add(f.ByteOffset), f.Name, resolveTypedef(f.Type), it.b.mem, it.b.hb)
		switch f.Name {
		case "tophash": // +rtype -fieldof bmap [8]uint8
			it.tophashes = field
		case "keys":
			it.keys = field
		case "values":
			it.values = field
		case "overflow":
			ptr, err := it.b.readPointer(field.Addr)
			if err != nil {
				logflags.DebuggerLogger().Errorf("could not load overflow variable: %v", err)
				return false
			}
			if it.overflow = s.findObject(Address(ptr), field.RealType.(*godwarf.PtrType).Type, proc.DereferenceMemory(it.b.mem)); it.overflow != nil {
				it.count += it.overflow.count
				it.size += it.overflow.size
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

func (s *ObjRefScope) next(it *mapIterator) bool {
	for {
		if it.b == nil {
			r := s.nextBucket(it)
			if !r {
				return false
			}
			it.idx = 0
		}
		if tophashesType, _ := it.tophashes.RealType.(*godwarf.ArrayType); it.idx >= tophashesType.Count {
			r := s.nextBucket(it)
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

func (it *mapIterator) key() *ReferenceVariable {
	k := it.keys.clone()
	k.RealType = resolveTypedef(k.RealType.(*godwarf.ArrayType).Type)
	k.Addr = k.Addr.Add(k.RealType.Size() * (it.idx - 1))
	// limit heap bits to a single key
	k.hb = newHeapBits(k.Addr, k.Addr.Add(k.RealType.Size()), k.hb.sp)
	return k
}

func (it *mapIterator) value() *ReferenceVariable {
	v := it.values.clone()
	v.RealType = resolveTypedef(v.RealType.(*godwarf.ArrayType).Type)
	v.Addr = v.Addr.Add(v.RealType.Size() * (it.idx - 1))
	// limit heap bits to a single value
	v.hb = newHeapBits(v.Addr, v.Addr.Add(v.RealType.Size()), v.hb.sp)
	return v
}

func (it *mapIterator) mapEvacuated(b *ReferenceVariable) bool {
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

func (s *ObjRefScope) readInterface(v *ReferenceVariable) (_type *proc.Variable, data *ReferenceVariable, isnil bool) {
	// An interface variable is implemented either by a runtime.iface
	// struct or a runtime.eface struct. The difference being that empty
	// interfaces (i.e. "interface {}") are represented by runtime.eface
	// and non-empty interfaces by runtime.iface.
	//
	// For both runtime.ifaces and runtime.efaces the data is stored in v.data
	//
	// The concrete type however is stored in v.tab._type for non-empty
	// interfaces and in v._type for empty interfaces.
	//
	// For nil empty interface variables _type will be nil, for nil
	// non-empty interface variables tab will be nil
	//
	// In either case the _type field is a pointer to a runtime._type struct.
	//
	// The following code works for both runtime.iface and runtime.eface.

	ityp := resolveTypedef(&v.RealType.(*godwarf.InterfaceType).TypedefType).(*godwarf.StructType)

	// +rtype -field iface.tab *itab|*internal/abi.ITab
	// +rtype -field iface.data unsafe.Pointer
	// +rtype -field eface._type *_type|*internal/abi.Type
	// +rtype -field eface.data unsafe.Pointer

	for _, f := range ityp.Field {
		switch f.Name {
		case "tab": // for runtime.iface
			ptr, err := readUintRaw(v.mem, uint64(v.Addr.Add(f.ByteOffset)), int64(s.bi.Arch.PtrSize()))
			if err != nil {
				logflags.DebuggerLogger().Errorf("read tab err: %v", err)
				continue
			}
			// +rtype *itab|*internal/abi.ITab
			tab := newReferenceVariable(Address(ptr), "", resolveTypedef(f.Type.(*godwarf.PtrType).Type), proc.DereferenceMemory(v.mem), nil)
			isnil = tab.Addr == 0
			if !isnil {
				for _, tf := range tab.RealType.(*godwarf.StructType).Field {
					switch tf.Name {
					case "Type":
						// +rtype *internal/abi.Type
						_type = newVariable("", uint64(tab.Addr.Add(tf.ByteOffset)), tf.Type, s.bi, tab.mem)
					case "_type":
						// +rtype *_type|*internal/abi.Type
						_type = newVariable("", uint64(tab.Addr.Add(tf.ByteOffset)), tf.Type, s.bi, tab.mem)
					}
				}
				if _type == nil {
					logflags.DebuggerLogger().Errorf("invalid interface type")
					return
				}
			}
		case "_type": // for runtime.eface
			_type = newVariable("", uint64(v.Addr.Add(f.ByteOffset)), f.Type, s.bi, v.mem)
			ptr, err := readUintRaw(v.mem, _type.Addr, int64(s.bi.Arch.PtrSize()))
			if err != nil {
				logflags.DebuggerLogger().Errorf("read tab err: %v", err)
				continue
			}
			isnil = ptr == 0
		case "data":
			data = newReferenceVariable(v.Addr.Add(f.ByteOffset), "", f.Type, v.mem, v.hb)
		}
	}
	return
}

func (v *ReferenceVariable) clone() *ReferenceVariable {
	r := *v
	return &r
}

// for special treatment to finalize pointers
type finalizePtrType struct {
	godwarf.Type
}

func readIntRaw(mem proc.MemoryReadWriter, addr uint64, size int64) (int64, error) {
	var n int64

	val := make([]byte, int(size))
	_, err := mem.ReadMemory(val, addr)
	if err != nil {
		return 0, err
	}

	switch size {
	case 1:
		n = int64(int8(val[0]))
	case 2:
		n = int64(int16(binary.LittleEndian.Uint16(val)))
	case 4:
		n = int64(int32(binary.LittleEndian.Uint32(val)))
	case 8:
		n = int64(binary.LittleEndian.Uint64(val))
	}

	return n, nil
}

func readUintRaw(mem proc.MemoryReadWriter, addr uint64, size int64) (uint64, error) {
	var n uint64

	val := make([]byte, int(size))
	_, err := mem.ReadMemory(val, addr)
	if err != nil {
		return 0, err
	}

	switch size {
	case 1:
		n = uint64(val[0])
	case 2:
		n = uint64(binary.LittleEndian.Uint16(val))
	case 4:
		n = uint64(binary.LittleEndian.Uint32(val))
	case 8:
		n = binary.LittleEndian.Uint64(val)
	}

	return n, nil
}

func readUint64Array(mem proc.MemoryReadWriter, addr uint64, res []uint64) (err error) {
	val := make([]byte, len(res)*8)
	_, err = mem.ReadMemory(val, addr)
	if err != nil {
		return
	}
	for i := 0; i < len(res); i++ {
		res[i] = binary.LittleEndian.Uint64(val[i*8 : (i+1)*8])
	}
	return
}

func readStringInfo(str *ReferenceVariable) (uint64, int64, error) {
	// string data structure is always two ptrs in size. Addr, followed by len
	// http://research.swtch.com/godata

	var strlen int64
	var outaddr uint64
	var err error

	for _, field := range str.RealType.(*godwarf.StringType).StructType.Field {
		switch field.Name {
		case "len":
			strlen, err = readIntRaw(str.mem, uint64(str.Addr.Add(field.ByteOffset)), 8)
			if err != nil {
				return 0, 0, fmt.Errorf("could not read string len %s", err)
			}
			if strlen < 0 {
				return 0, 0, fmt.Errorf("invalid length: %d", strlen)
			}
		case "str":
			outaddr, err = str.readPointer(str.Addr.Add(field.ByteOffset))
			if err != nil {
				return 0, 0, fmt.Errorf("could not read string pointer %s", err)
			}
			if outaddr == 0 {
				return 0, 0, nil
			}
		}
	}

	return outaddr, strlen, nil
}

// alignAddr rounds up addr to a multiple of align. Align must be a power of 2.
func alignAddr(addr, align int64) int64 {
	return (addr + align - 1) &^ (align - 1)
}

func fakeArrayType(n uint64, fieldType godwarf.Type) godwarf.Type {
	stride := alignAddr(fieldType.Common().ByteSize, fieldType.Align())
	return &godwarf.ArrayType{
		CommonType: godwarf.CommonType{
			ReflectKind: reflect.Array,
			ByteSize:    int64(n) * stride,
			Name:        fmt.Sprintf("[%d]%s", n, fieldType.String()),
		},
		Type:          fieldType,
		StrideBitSize: stride * 8,
		Count:         int64(n),
	}
}

func pointerTo(typ godwarf.Type, arch *proc.Arch) godwarf.Type {
	return &godwarf.PtrType{
		CommonType: godwarf.CommonType{
			ByteSize:    int64(arch.PtrSize()),
			Name:        "*" + typ.Common().Name,
			ReflectKind: reflect.Ptr,
			Offset:      0,
		},
		Type: typ,
	}
}

func resolveTypedef(typ godwarf.Type) godwarf.Type {
	for {
		switch tt := typ.(type) {
		case *godwarf.TypedefType:
			typ = tt.Type
		case *godwarf.QualType:
			typ = tt.Type
		default:
			return typ
		}
	}
}
