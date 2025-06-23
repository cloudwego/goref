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
	"fmt"
	"reflect"
	"strconv"
	"sync"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
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

var rvpool = sync.Pool{
	New: func() interface{} {
		return &ReferenceVariable{}
	},
}

// ReferenceVariable represents a variable. It contains the address, name,
// type and other information parsed from both the Dwarf information
// and the memory of the debugged process.
type ReferenceVariable struct {
	Addr     Address
	Name     string
	RealType godwarf.Type
	mem      proc.MemoryReadWriter

	// heap bits for this object
	hb *gcMaskBitIterator

	// node size
	size int64
	// node count
	count int64
}

func newReferenceVariable(addr Address, name string, typ godwarf.Type, mem proc.MemoryReadWriter, hb *gcMaskBitIterator) *ReferenceVariable {
	rv := rvpool.Get().(*ReferenceVariable)
	rv.Addr, rv.Name, rv.RealType, rv.mem, rv.hb, rv.size, rv.count = addr, name, typ, mem, hb, 0, 0
	return rv
}

func newReferenceVariableWithSizeAndCount(addr Address, name string, typ godwarf.Type, mem proc.MemoryReadWriter, hb *gcMaskBitIterator, size, count int64) *ReferenceVariable {
	rv := newReferenceVariable(addr, name, typ, mem, hb)
	rv.size, rv.count = size, count
	return rv
}

func (v *ReferenceVariable) dereference(s *ObjRefScope) *ReferenceVariable {
	switch t := v.RealType.(type) {
	case *godwarf.PtrType:
		ptr, err := v.readPointer(v.Addr)
		if err != nil {
			return nil
		}
		nv := s.findObject(Address(ptr), resolveTypedef(t.Type), proc.DereferenceMemory(v.mem))
		return nv
	default:
		return nil
	}
}

func (v *ReferenceVariable) toField(field *godwarf.StructField) *ReferenceVariable {
	fieldAddr := v.Addr.Add(field.ByteOffset)
	return newReferenceVariable(fieldAddr, field.Name+". ("+field.Type.String()+")", resolveTypedef(field.Type), v.mem, v.hb)
}

func (v *ReferenceVariable) arrayAccess(idx int64) *ReferenceVariable {
	at, ok := v.RealType.(*godwarf.ArrayType)
	if !ok {
		return nil
	}
	elemAddr := v.Addr.Add(idx * at.Type.Size())
	// collapse 10+ elements by default
	name := "[10+]"
	if idx < 10 {
		name = "[" + strconv.Itoa(int(idx)) + "]"
	}
	return newReferenceVariable(elemAddr, name+". ("+at.Type.String()+")", resolveTypedef(at.Type), v.mem, v.hb)
}

func (v *ReferenceVariable) arrayLen() uint64 {
	at, ok := v.RealType.(*godwarf.ArrayType)
	if !ok {
		return 0
	}
	return uint64(at.Count)
}

func (v *ReferenceVariable) asInt() (int64, error) {
	return readIntRaw(v.mem, uint64(v.Addr), 8)
}

func (v *ReferenceVariable) asUint() (uint64, error) {
	return readUintRaw(v.mem, uint64(v.Addr), 8)
}

func (v *ReferenceVariable) readPointer(addr Address) (uint64, error) {
	if err := v.hb.resetGCMask(addr); err != nil {
		return 0, err
	}
	return readUintRaw(v.mem, uint64(addr), 8)
}

func (v *ReferenceVariable) readUint64(addr Address) (uint64, error) {
	if v.hb != nil {
		if addr < v.hb.base || addr >= v.hb.end {
			return 0, errOutOfRange
		}
	}
	return readUintRaw(v.mem, uint64(addr), 8)
}

func (s *ObjRefScope) readInterface(v *ReferenceVariable) (_type *proc.Variable, data *ReferenceVariable) {
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
			ptr, err := v.readUint64(v.Addr.Add(f.ByteOffset))
			if err != nil {
				continue
			}
			// +rtype *itab|*internal/abi.ITab
			if ptr != 0 {
				for _, tf := range resolveTypedef(f.Type.(*godwarf.PtrType).Type).(*godwarf.StructType).Field {
					switch tf.Name {
					case "Type":
						// +rtype *internal/abi.Type
						_type = newVariable("", uint64(Address(ptr).Add(tf.ByteOffset)), tf.Type, s.bi, proc.DereferenceMemory(v.mem))
					case "_type":
						// +rtype *_type|*internal/abi.Type
						_type = newVariable("", uint64(Address(ptr).Add(tf.ByteOffset)), tf.Type, s.bi, proc.DereferenceMemory(v.mem))
					}
				}
				if _type == nil {
					logflags.DebuggerLogger().Errorf("invalid interface type")
				}
			}
		case "_type": // for runtime.eface
			_type = newVariable("", uint64(v.Addr.Add(f.ByteOffset)), f.Type, s.bi, v.mem)
		case "data":
			data = newReferenceVariable(v.Addr.Add(f.ByteOffset), "", f.Type, v.mem, v.hb)
		}
	}
	return
}

func (v *ReferenceVariable) clone() *ReferenceVariable {
	return newReferenceVariable(v.Addr, v.Name, v.RealType, v.mem, v.hb)
}

func ToReferenceVariable(v *proc.Variable) *ReferenceVariable {
	return newReferenceVariable(Address(v.Addr), v.Name, v.RealType, getVariableMem(v), nil)
}

func (v *ReferenceVariable) ToDelveVariable(bi *proc.BinaryInfo) *proc.Variable {
	return newVariable(v.Name, uint64(v.Addr), v.RealType, bi, v.mem)
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

func readStringInfo(str *ReferenceVariable) (addr, strlen uint64, err error) {
	// string data structure is always two ptrs in size. Addr, followed by len
	// http://research.swtch.com/godata
	for _, field := range str.RealType.(*godwarf.StringType).StructType.Field {
		switch field.Name {
		case "len":
			strlen, _ = str.readUint64(str.Addr.Add(field.ByteOffset))
		case "str":
			addr, err = str.readPointer(str.Addr.Add(field.ByteOffset))
			if err != nil {
				return 0, 0, err
			}
		}
	}

	return addr, strlen, nil
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

func CeilDivide(n, d int64) int64 {
	r := n / d
	if n%d > 0 {
		r++
	}
	return r
}

func CeilDivide2(n, d1, d2 int64) int64 {
	r1 := n / d1
	if n%d1 > 0 {
		r1++
	}
	r2 := r1 / d2
	if r1%d2 > 0 {
		r2++
	}
	return r2
}
