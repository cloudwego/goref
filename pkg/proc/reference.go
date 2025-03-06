// Copyright 2024 CloudWeGo Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package proc

import (
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"regexp"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/reader"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
)

const (
	maxRefDepth           = 256
	disableDwarfSearching = false
)

type ObjRefScope struct {
	*HeapScope

	pb *profileBuilder

	// maybe nil
	g *stack
}

func (s *ObjRefScope) findObject(addr Address, typ godwarf.Type, mem proc.MemoryReadWriter) (v *ReferenceVariable) {
	sp, base := s.findSpanAndBase(addr)
	if sp == nil {
		// not in heap
		var end Address
		if suc, seg := s.bss.mark(addr); suc {
			// in bss segment
			end = seg.end
		} else if suc, seg = s.data.mark(addr); suc {
			// in data segment
			end = seg.end
		} else if s.g != nil && s.g.mark(addr) {
			// in g stack
			end = s.g.end
		} else {
			return
		}
		if addr.Add(typ.Size()) > end {
			// There is an unsafe conversion, it is certain that another root object
			// is referencing the memory, so there is no need to scan this object.
			return
		}
		v = newReferenceVariable(addr, "", resolveTypedef(typ), mem, nil)
		return
	}
	// Find mark bit
	if !sp.mark(base) {
		return // already found
	}
	realBase := s.copyGCMask(sp, base)

	// heap bits searching
	hb := newGCBitsIterator(realBase, sp.elemEnd(base), sp.base, sp.ptrMask)
	if hb.nextPtr(false) != 0 {
		// has pointer, cache mem
		mem = cacheMemory(mem, uint64(base), int(sp.elemSize))
	}
	v = newReferenceVariableWithSizeAndCount(addr, "", resolveTypedef(typ), mem, hb, sp.elemSize, 1)
	return
}

func (s *HeapScope) markObject(addr Address, mem proc.MemoryReadWriter) (size, count int64) {
	sp, base := s.findSpanAndBase(addr)
	if sp == nil {
		return // not found
	}
	// Find mark bit
	if !sp.mark(base) {
		return // already found
	}
	realBase := s.copyGCMask(sp, base)
	size, count = sp.elemSize, 1
	hb := newGCBitsIterator(realBase, sp.elemEnd(base), sp.base, sp.ptrMask)
	var cmem proc.MemoryReadWriter
	for {
		ptr := hb.nextPtr(true)
		if ptr == 0 {
			break
		}
		if cmem == nil {
			cmem = cacheMemory(mem, uint64(ptr), int(hb.end.Sub(ptr)))
		}
		nptr, err := readUintRaw(cmem, uint64(ptr), int64(s.bi.Arch.PtrSize()))
		if err != nil {
			continue
		}
		size_, count_ := s.markObject(Address(nptr), cmem)
		size += size_
		count += count_
	}
	return
}

func (s *ObjRefScope) record(idx *pprofIndex, size, count int64) {
	if size == 0 && count == 0 {
		return
	}
	s.pb.addReference(idx.indexes(), count, size)
}

type finalMarkParam struct {
	idx *pprofIndex
	hb  *gcMaskBitIterator
}

func (s *ObjRefScope) finalMark(idx *pprofIndex, hb *gcMaskBitIterator) {
	var ptr Address
	var size, count int64
	var cmem proc.MemoryReadWriter
	for {
		ptr = hb.nextPtr(true)
		if ptr == 0 {
			break
		}
		if cmem == nil {
			cmem = cacheMemory(s.mem, uint64(ptr), int(hb.end.Sub(ptr)))
		}
		ptr, err := readUintRaw(cmem, uint64(ptr), int64(s.bi.Arch.PtrSize()))
		if err != nil {
			continue
		}
		size_, count_ := s.markObject(Address(ptr), cmem)
		size += size_
		count += count_
	}
	s.record(idx, size, count)
}

// findRef finds sub refs of x, and records them to pprof buffer.
func (s *ObjRefScope) findRef(x *ReferenceVariable, idx *pprofIndex) (err error) {
	if x.Name != "" {
		if idx != nil && idx.depth >= maxRefDepth {
			// No scan for depth >= maxRefDepth, as it could lead to uncontrollable reference chain depths.
			// No need to worry about memory not being able to be recorded, as the parent object will be finally scanned.
			return
		}
		// For array elem / map kv / struct field type, record them.
		idx = idx.pushHead(s.pb, x.Name)
		defer func() { s.record(idx, x.size, x.count) }()
	} else {
		// For newly found heap objects, check if all pointers have been scanned by the DWARF searching.
		defer func() {
			if x.hb.nextPtr(false) != 0 {
				// still has pointer, add to the finalMarks
				s.finalMarks = append(s.finalMarks, finalMarkParam{idx, x.hb})
			}
		}()
	}
	switch typ := x.RealType.(type) {
	case *godwarf.PtrType:
		var ptrval uint64
		ptrval, err = x.readPointer(x.Addr)
		if err != nil {
			return
		}
		if y := s.findObject(Address(ptrval), resolveTypedef(typ.Type), proc.DereferenceMemory(x.mem)); y != nil {
			_ = s.findRef(y, idx)
			// flatten reference
			x.size += y.size
			x.count += y.count
		}
	case *godwarf.ChanType:
		var ptrval uint64
		ptrval, err = x.readPointer(x.Addr)
		if err != nil {
			return
		}
		if y := s.findObject(Address(ptrval), resolveTypedef(typ.Type.(*godwarf.PtrType).Type), proc.DereferenceMemory(x.mem)); y != nil {
			x.size += y.size
			x.count += y.count

			structType, ok := y.RealType.(*godwarf.StructType)
			if !ok {
				return
			}
			var zptrval, chanLen uint64
			for _, field := range structType.Field {
				switch field.Name {
				case "buf":
					zptrval, err = y.readPointer(y.Addr.Add(field.ByteOffset))
					if err != nil {
						return
					}
				case "dataqsiz":
					chanLen, _ = y.readUint64(y.Addr.Add(field.ByteOffset))
				}
			}
			if z := s.findObject(Address(zptrval), fakeArrayType(chanLen, typ.ElemType), y.mem); z != nil {
				_ = s.findRef(z, idx)
				x.size += z.size
				x.count += z.count
			}
		}
	case *godwarf.MapType:
		var ptrval uint64
		ptrval, err = x.readPointer(x.Addr)
		if err != nil {
			return
		}
		if y := s.findObject(Address(ptrval), resolveTypedef(typ.Type.(*godwarf.PtrType).Type), proc.DereferenceMemory(x.mem)); y != nil {
			var it mapIterator
			it, err = s.toMapIterator(y, typ.KeyType, typ.ElemType)
			if err != nil {
				// logflags.DebuggerLogger().Errorf("toMapIterator failed: %v", err)
				return
			}
			for it.next(s) {
				// find key ref
				if key := it.key(); key != nil {
					key.Name = "$mapkey. (" + key.RealType.String() + ")"
					if err := s.findRef(key, idx); errors.Is(err, errOutOfRange) {
						continue
					}
				}
				// find val ref
				if val := it.value(); val != nil {
					val.Name = "$mapval. (" + val.RealType.String() + ")"
					if err := s.findRef(val, idx); errors.Is(err, errOutOfRange) {
						continue
					}
				}
			}
			// avoid missing memory
			objects, size, count := it.referenceInfo()
			for _, obj := range objects {
				if obj.hb.nextPtr(false) != 0 {
					// still has pointer, add to the finalMarks
					s.finalMarks = append(s.finalMarks, finalMarkParam{idx, obj.hb})
				}
			}
			x.size += size
			x.count += count
		}
	case *godwarf.StringType:
		var strAddr, strLen uint64
		strAddr, strLen, err = readStringInfo(x)
		if err != nil {
			return
		}
		if y := s.findObject(Address(strAddr), fakeArrayType(strLen, &godwarf.UintType{BasicType: godwarf.BasicType{CommonType: godwarf.CommonType{ByteSize: 1, Name: "byte", ReflectKind: reflect.Uint8}, BitSize: 8, BitOffset: 0}}), proc.DereferenceMemory(x.mem)); y != nil {
			_ = s.findRef(y, idx)
			x.size += y.size
			x.count += y.count
		}
	case *godwarf.SliceType:
		var base, cap_ uint64
		for _, f := range typ.Field {
			switch f.Name {
			case "array":
				base, err = x.readPointer(x.Addr.Add(f.ByteOffset))
				if err != nil {
					return
				}
			case "cap":
				cap_, _ = x.readUint64(x.Addr.Add(f.ByteOffset))
			}
		}
		if y := s.findObject(Address(base), fakeArrayType(cap_, typ.ElemType), proc.DereferenceMemory(x.mem)); y != nil {
			_ = s.findRef(y, idx)
			x.size += y.size
			x.count += y.count
		}
	case *godwarf.InterfaceType:
		_type, data := s.readInterface(x)
		if data == nil {
			return
		}
		var ptrval uint64
		ptrval, err = data.readPointer(data.Addr)
		if err != nil || ptrval == 0 {
			return
		}
		var ityp godwarf.Type
		if _type != nil {
			var rtyp godwarf.Type
			var kind int64
			rtyp, kind, err = proc.RuntimeTypeToDIE(_type, uint64(data.Addr), s.mds)
			if err == nil {
				if kind&kindDirectIface == 0 {
					if _, isptr := resolveTypedef(rtyp).(*godwarf.PtrType); !isptr {
						rtyp = pointerTo(rtyp, s.bi.Arch)
					}
				}
				if ptrType, isPtr := resolveTypedef(rtyp).(*godwarf.PtrType); isPtr {
					ityp = resolveTypedef(ptrType.Type)
				}
			}
		}
		if ityp == nil {
			ityp = new(godwarf.VoidType)
		}
		if y := s.findObject(Address(ptrval), ityp, proc.DereferenceMemory(x.mem)); y != nil {
			_ = s.findRef(y, idx)
			x.size += y.size
			x.count += y.count
		}
	case *godwarf.StructType:
		typ = s.specialStructTypes(typ)
		for _, field := range typ.Field {
			y := x.toField(field)
			if err = s.findRef(y, idx); errors.Is(err, errOutOfRange) {
				break
			}
		}
	case *godwarf.ArrayType:
		eType := resolveTypedef(typ.Type)
		if !hasPtrType(eType) {
			return
		}
		for i := int64(0); i < typ.Count; i++ {
			y := x.arrayAccess(i)
			if err = s.findRef(y, idx); errors.Is(err, errOutOfRange) {
				break
			}
		}
	case *godwarf.FuncType:
		var closureAddr uint64
		closureAddr, err = x.readPointer(x.Addr)
		if err != nil || closureAddr == 0 {
			return
		}
		var cst godwarf.Type
		var funcAddr uint64
		funcAddr, err = readUintRaw(proc.DereferenceMemory(x.mem), closureAddr, int64(s.bi.Arch.PtrSize()))
		if err == nil && funcAddr != 0 {
			if fn := s.bi.PCToFunc(funcAddr); fn != nil {
				cst = s.closureStructType(fn)
			}
		}
		if cst == nil {
			cst = new(godwarf.VoidType)
		}
		if closure := s.findObject(Address(closureAddr), cst, proc.DereferenceMemory(x.mem)); closure != nil {
			_ = s.findRef(closure, idx)
			x.size += closure.size
			x.count += closure.count
		}
	case *finalizePtrType:
		if y := s.findObject(x.Addr, new(godwarf.VoidType), x.mem); y != nil {
			_ = s.findRef(y, idx)
			x.size += y.size
			x.count += y.count
		}
	default:
	}
	return
}

func (s *ObjRefScope) closureStructType(fn *proc.Function) *godwarf.StructType {
	var fe funcExtra
	if fe = s.funcExtraMap[fn]; fe.closureStructType != nil {
		return fe.closureStructType
	}
	image := funcToImage(s.bi, fn)
	dwarfTree, err := getDwarfTree(image, getFunctionOffset(fn))
	if err != nil {
		return nil
	}
	st := &godwarf.StructType{
		Kind: "struct",
	}
	vars := reader.Variables(dwarfTree, 0, 0, reader.VariablesNoDeclLineCheck|reader.VariablesSkipInlinedSubroutines)
	for _, v := range vars {
		off, ok := v.Val(godwarf.AttrGoClosureOffset).(int64)
		if ok {
			n, typ, err := readVarEntry(v.Tree, image)
			if err == nil {
				if len(n) > 0 && n[0] == '&' {
					// escaped variables
					n = n[1:]
				}
				sz := typ.Common().ByteSize
				st.Field = append(st.Field, &godwarf.StructField{
					Name:       n,
					Type:       typ,
					ByteOffset: off,
					ByteSize:   sz,
					BitOffset:  off * 8,
					BitSize:    sz * 8,
				})
			}
		}
	}

	if len(st.Field) > 0 {
		lf := st.Field[len(st.Field)-1]
		st.ByteSize = lf.ByteOffset + lf.Type.Common().ByteSize
	}
	fe.closureStructType = st
	s.funcExtraMap[fn] = fe
	return st
}

var atomicPointerRegex = regexp.MustCompile(`^sync/atomic\.Pointer\[.*\]$`)

func (s *ObjRefScope) specialStructTypes(st *godwarf.StructType) *godwarf.StructType {
	switch {
	case atomicPointerRegex.MatchString(st.StructName):
		// v *sync.readOnly
		nst := *st
		nst.Field = make([]*godwarf.StructField, len(st.Field))
		copy(nst.Field, st.Field)
		nf := *nst.Field[2]
		nf.Type = nst.Field[0].Type.(*godwarf.ArrayType).Type
		nst.Field[2] = &nf
		return &nst
	}
	return st
}

func hasPtrType(t godwarf.Type) bool {
	switch typ := t.(type) {
	case *godwarf.PtrType, *godwarf.ChanType, *godwarf.MapType, *godwarf.StringType,
		*godwarf.SliceType, *godwarf.InterfaceType, *godwarf.FuncType:
		return true
	case *godwarf.StructType:
		for _, f := range typ.Field {
			if hasPtrType(resolveTypedef(f.Type)) {
				return true
			}
		}
	case *godwarf.ArrayType:
		return hasPtrType(resolveTypedef(typ.Type))
	}
	return false
}

var loadSingleValue = proc.LoadConfig{}

// ObjectReference scanning goroutine stack and global vars to search all heap objects they reference,
// and outputs the reference relationship to the filename with pprof format.
func ObjectReference(t *proc.Target, filename string) error {
	scope, err := proc.ThreadScope(t, t.CurrentThread())
	if err != nil {
		return err
	}

	heapScope := &HeapScope{mem: t.Memory(), bi: t.BinInfo(), scope: scope, funcExtraMap: make(map[*proc.Function]funcExtra)}
	err = heapScope.readHeap()
	if err != nil {
		return err
	}

	f, err := os.Create(filename)
	if err != nil {
		return err
	}

	s := &ObjRefScope{
		HeapScope: heapScope,
		pb:        newProfileBuilder(f),
	}

	mds, err := proc.LoadModuleData(t.BinInfo(), t.Memory())
	if err != nil {
		return err
	}
	s.mds = mds

	// Global variables
	pvs, _ := scope.PackageVariables(loadSingleValue)
	for _, pv := range pvs {
		if pv.Addr == 0 || disableDwarfSearching {
			continue
		}
		s.findRef(newReferenceVariable(Address(pv.Addr), pv.Name, pv.RealType, t.Memory(), nil), nil)
	}

	// Local variables
	threadID := t.CurrentThread().ThreadID()
	grs, _, _ := proc.GoroutinesInfo(t, 0, 0)
	for _, gr := range grs {
		s.g = &stack{}
		lo, hi := getStack(gr)
		if gr.Thread != nil {
			threadID = gr.Thread.ThreadID()
		}
		sf, _ := proc.GoroutineStacktrace(t, gr, 1024, 0)
		s.g.init(Address(lo), Address(hi), s.stackPtrMask(Address(lo), Address(hi), sf))
		if len(sf) > 0 {
			for i := range sf {
				ms := myEvalScope{EvalScope: *proc.FrameToScope(t, t.Memory(), gr, threadID, sf[i:]...)}
				locals, err := ms.Locals(t, gr, threadID, mds)
				if err != nil {
					logflags.DebuggerLogger().Warnf("local variables err: %v", err)
					continue
				}
				for _, l := range locals {
					if l.Addr == 0 || disableDwarfSearching {
						continue
					}
					if l.Name[0] == '&' {
						// escaped variables
						l.Name = l.Name[1:]
					}
					l.Name = sf[i].Current.Fn.Name + "." + l.Name
					s.findRef(l, nil)
				}
			}
		}
		// scan root gc bits in case dwarf searching failure
		for _, fr := range s.g.frames {
			it := &(fr.gcMaskBitIterator)
			if it.nextPtr(false) != 0 {
				// add to the finalMarks
				idx := (*pprofIndex)(nil).pushHead(s.pb, fr.funcName)
				s.finalMarks = append(s.finalMarks, finalMarkParam{idx, it})
			}
		}
	}
	s.g = nil

	// final mark segment root bits
	for i, seg := range s.bss {
		it := &(seg.gcMaskBitIterator)
		if it.nextPtr(false) != 0 {
			idx := (*pprofIndex)(nil).pushHead(s.pb, fmt.Sprintf("bss segment[%d]", i))
			s.finalMarks = append(s.finalMarks, finalMarkParam{idx, it})
		}
	}
	for i, seg := range s.data {
		it := &(seg.gcMaskBitIterator)
		if it.nextPtr(false) != 0 {
			idx := (*pprofIndex)(nil).pushHead(s.pb, fmt.Sprintf("data segment[%d]", i))
			s.finalMarks = append(s.finalMarks, finalMarkParam{idx, it})
		}
	}

	// Finalizers
	for _, fin := range heapScope.finalizers {
		// scan object
		s.findRef(newReferenceVariable(fin.p, "finalized", new(finalizePtrType), s.mem, nil), nil)
		// scan finalizer
		s.findRef(newReferenceVariable(fin.fn, "finalizer", new(godwarf.FuncType), s.mem, nil), nil)
	}

	for _, param := range s.finalMarks {
		s.finalMark(param.idx, param.hb)
	}

	s.pb.flush()
	log.Printf("successfully output to `%s`\n", filename)
	return nil
}
