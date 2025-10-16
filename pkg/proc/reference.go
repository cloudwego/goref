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
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
)

// display memory space which doesn't match the dwarf type definition
// e.g.
// var arr [16]string = [...]string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p"}
// var p *string = &arr[0]
// return p
// The sub objects of arr[1:] never be scanned if the dwarf type definition of *string is returned,
// so we need to name the sub objects as subObjectsName to display them correctly.
const subObjectsName = "$sub_objects$"

// the max reference depth shown by pprof
var maxRefDepth = 256

func SetMaxRefDepth(depth int) {
	maxRefDepth = depth
}

type ObjRefScope struct {
	*HeapScope

	pb *profileBuilder

	// maybe nil
	g *stack
}

// GetProfileDataForTest exports all internal profile data for testing purposes.
// This allows direct validation of reference analysis without protobuf parsing.
func (s *ObjRefScope) GetProfileDataForTest() (nodes map[string]*profileNode, strings []string, stringMap map[string]int) {
	return s.pb.nodes, s.pb.strings, s.pb.stringMap
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
		v = newReferenceVariable(addr, "", godwarf.ResolveTypedef(typ), mem, nil)
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
	v = newReferenceVariableWithSizeAndCount(addr, "", godwarf.ResolveTypedef(typ), mem, hb, sp.elemSize, 1)
	return
}

func (s *HeapScope) markObject(addr Address, mem proc.MemoryReadWriter) (size, count int64) {
	type stackEntry struct {
		addr Address
	}
	var stack []stackEntry
	stack = append(stack, stackEntry{addr})

	for len(stack) > 0 {
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		addr := entry.addr

		sp, base := s.findSpanAndBase(addr)
		if sp == nil {
			continue // not found
		}
		// Find mark bit
		if !sp.mark(base) {
			continue // already found
		}
		realBase := s.copyGCMask(sp, base)
		size += sp.elemSize
		count++

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
			stack = append(stack, stackEntry{Address(nptr)})
		}
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
			// Don't worry about memory can't be recorded, as the parent object will be finally scanned.
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
				s.finalMarks = append(s.finalMarks, finalMarkParam{idx.pushHead(s.pb, subObjectsName), x.hb})
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
		if y := s.findObject(Address(ptrval), godwarf.ResolveTypedef(typ.Type), proc.DereferenceMemory(x.mem)); y != nil {
			_ = s.findRef(y, idx)
			// flatten reference
			x.size += y.size
			x.count += y.count
			rvpool.Put(y)
		}
	case *godwarf.ChanType:
		var ptrval uint64
		ptrval, err = x.readPointer(x.Addr)
		if err != nil {
			return
		}
		if y := s.findObject(Address(ptrval), godwarf.ResolveTypedef(typ.Type.(*godwarf.PtrType).Type), proc.DereferenceMemory(x.mem)); y != nil {
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
				rvpool.Put(z)
			}
			rvpool.Put(y)
		}
	case *godwarf.MapType:
		var ptrval uint64
		ptrval, err = x.readPointer(x.Addr)
		if err != nil {
			return
		}
		if y := s.findObject(Address(ptrval), godwarf.ResolveTypedef(typ.Type.(*godwarf.PtrType).Type), proc.DereferenceMemory(x.mem)); y != nil {
			var it mapIterator
			it, err = s.toMapIterator(y, typ.KeyType, typ.ElemType)
			if err == nil {
				for it.next(s) {
					// find key ref
					if key := it.key(); key != nil {
						key.Name = "$mapkey. (" + key.RealType.String() + ")"
						if err := s.findRef(key, idx); errors.Is(err, errOutOfRange) {
							continue
						}
						rvpool.Put(key)
					}
					// find val ref
					if val := it.value(); val != nil {
						val.Name = "$mapval. (" + val.RealType.String() + ")"
						if err := s.findRef(val, idx); errors.Is(err, errOutOfRange) {
							continue
						}
						rvpool.Put(val)
					}
				}
			}
			if it != nil {
				// avoid missing memory
				objects, size, count := it.referenceInfo()
				for _, obj := range objects {
					if obj.hb.nextPtr(false) != 0 {
						// still has pointer, add to the finalMarks
						s.finalMarks = append(s.finalMarks, finalMarkParam{idx.pushHead(s.pb, subObjectsName), obj.hb})
					}
				}
				x.size += size
				x.count += count
			}
			rvpool.Put(y)
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
			rvpool.Put(y)
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
			rvpool.Put(y)
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
			var directIface bool
			rtyp, directIface, err = proc.RuntimeTypeToDIE(_type, uint64(data.Addr), s.mds)
			if err == nil {
				if !directIface {
					if _, isptr := godwarf.ResolveTypedef(rtyp).(*godwarf.PtrType); !isptr {
						rtyp = pointerTo(rtyp, s.bi.Arch)
					}
				}
				if ptrType, isPtr := godwarf.ResolveTypedef(rtyp).(*godwarf.PtrType); isPtr {
					ityp = godwarf.ResolveTypedef(ptrType.Type)
				}
			}
		}
		rvpool.Put(data)
		if ityp == nil {
			ityp = new(godwarf.VoidType)
		}
		if y := s.findObject(Address(ptrval), ityp, proc.DereferenceMemory(x.mem)); y != nil {
			_ = s.findRef(y, idx)
			x.size += y.size
			x.count += y.count
			rvpool.Put(y)
		}
	case *godwarf.StructType:
		typ = s.specialStructTypes(typ)
		for _, field := range typ.Field {
			y := x.toField(field)
			err = s.findRef(y, idx)
			rvpool.Put(y)
			if errors.Is(err, errOutOfRange) {
				break
			}
		}
	case *godwarf.ArrayType:
		eType := godwarf.ResolveTypedef(typ.Type)
		if !hasPtrType(eType) {
			return
		}
		for i := int64(0); i < typ.Count; i++ {
			y := x.arrayAccess(i)
			err = s.findRef(y, idx)
			rvpool.Put(y)
			if errors.Is(err, errOutOfRange) {
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
				cst = funcExtra(fn, s.bi).closureStructType
			}
		}
		if cst == nil {
			cst = new(godwarf.VoidType)
		}
		if closure := s.findObject(Address(closureAddr), cst, proc.DereferenceMemory(x.mem)); closure != nil {
			_ = s.findRef(closure, idx)
			x.size += closure.size
			x.count += closure.count
			rvpool.Put(closure)
		}
	case *finalizePtrType:
		if y := s.findObject(x.Addr, new(godwarf.VoidType), x.mem); y != nil {
			_ = s.findRef(y, idx)
			x.size += y.size
			x.count += y.count
			rvpool.Put(y)
		}
	default:
	}
	return
}

var (
	atomicPointerRegex = regexp.MustCompile(`^sync/atomic\.Pointer\[.*\]$`)

	atomicPointerReplacingMap = map[*godwarf.StructType]*godwarf.StructType{}

	// type of sync/atomic.Pointer[internal/sync.entry[interface {},interface {}]]
	entryPtrTypeInit bool
	entryPtrType     *godwarf.StructType
)

func (s *ObjRefScope) specialStructTypes(st *godwarf.StructType) *godwarf.StructType {
	if st.StructName == "sync/atomic.Pointer[internal/sync.node[interface {},interface {}]]" {
		// goexperiment.synchashtriemap
		// replace `internal/sync.node` by `sync/atomic.entry`
		if !entryPtrTypeInit {
			entryPtrTypeInit = true
			tmp, _ := findType(s.bi, "sync/atomic.Pointer[internal/sync.entry[interface {},interface {}]]")
			entryPtrType, _ = tmp.(*godwarf.StructType)
		}
		if entryPtrType != nil {
			st = entryPtrType
		}
	}
	switch {
	case atomicPointerRegex.MatchString(st.StructName):
		// v *sync.readOnly
		if nst := atomicPointerReplacingMap[st]; nst != nil {
			return nst
		}
		nst := *st
		nst.Field = make([]*godwarf.StructField, len(st.Field))
		copy(nst.Field, st.Field)
		nf := *nst.Field[2]
		nf.Type = nst.Field[0].Type.(*godwarf.ArrayType).Type
		nst.Field[2] = &nf
		atomicPointerReplacingMap[st] = &nst
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
			if hasPtrType(godwarf.ResolveTypedef(f.Type)) {
				return true
			}
		}
	case *godwarf.ArrayType:
		return hasPtrType(godwarf.ResolveTypedef(typ.Type))
	}
	return false
}

var loadSingleValue = proc.LoadConfig{}

// ObjectReference scanning goroutine stack and global vars to search all heap objects they reference,
// and outputs the reference relationship to the filename with pprof format.
// Returns the ObjRefScope for testing purposes.
func ObjectReference(t *proc.Target, filename string) (*ObjRefScope, error) {
	scope, err := proc.ThreadScope(t, t.CurrentThread())
	if err != nil {
		return nil, err
	}

	heapScope := &HeapScope{mem: t.Memory(), bi: t.BinInfo(), scope: scope}
	err = heapScope.readHeap()
	if err != nil {
		return nil, err
	}

	f, err := os.Create(filename)
	if err != nil {
		return nil, err
	}

	s := &ObjRefScope{
		HeapScope: heapScope,
		pb:        newProfileBuilder(f),
	}

	mds, err := getModuleData(t.BinInfo(), t.Memory())
	if err != nil {
		return nil, err
	}
	s.mds = mds

	// Global variables
	pvs, _ := scope.PackageVariables(loadSingleValue)
	for _, pv := range pvs {
		if pv.Addr == 0 {
			continue
		}
		rv := ToReferenceVariable(pv)
		s.findRef(rv, nil)
		rvpool.Put(rv)
	}

	// Local variables
	var allgs []*stack
	threadID := t.CurrentThread().ThreadID()
	grs, _, _ := proc.GoroutinesInfo(t, 0, 0)
	for _, gr := range grs {
		if gr.Unreadable != nil {
			logflags.DebuggerLogger().Warnf("unreadable goroutine err: %v", gr.Unreadable)
			continue
		}
		s.g = &stack{}
		lo, hi := getStack(gr)
		if gr.Thread != nil {
			threadID = gr.Thread.ThreadID()
		}
		sf, _ := proc.GoroutineStacktrace(t, gr, 1024, 0)
		s.g.init(Address(lo), Address(hi), s.stackPtrMask(Address(lo), Address(hi), sf))
		if len(sf) > 0 {
			for i := range sf {
				es := proc.FrameToScope(t, t.Memory(), gr, threadID, sf[i:]...)
				locals, err := es.Locals(0, "")
				if err != nil {
					logflags.DebuggerLogger().Warnf("local variables err: %v", err)
					continue
				}
				for _, l := range locals {
					if l.Addr == 0 {
						continue
					}
					l.Name = sf[i].Current.Fn.Name + "." + l.Name
					rv := ToReferenceVariable(l)
					s.findRef(rv, nil)
					rvpool.Put(rv)
				}
			}
		}
		allgs = append(allgs, s.g)
		s.g = nil
	}

	// Finalizers
	for _, fin := range heapScope.finalizers {
		// scan object
		rv := newReferenceVariable(fin.p, "runtime.SetFinalizer.obj", new(finalizePtrType), s.mem, nil)
		s.findRef(rv, nil)
		rvpool.Put(rv)
		// scan finalizer
		rv = newReferenceVariable(fin.fn, "runtime.SetFinalizer.fn", new(godwarf.FuncType), s.mem, nil)
		s.findRef(rv, nil)
		rvpool.Put(rv)
	}
	// Cleanups
	for _, clu := range heapScope.cleanups {
		// scan cleanup
		rv := newReferenceVariable(clu.fn, "runtime.AddCleanup.fn", new(godwarf.FuncType), s.mem, nil)
		s.findRef(rv, nil)
		rvpool.Put(rv)
	}

	// final mark with gc mask bits
	for _, param := range s.finalMarks {
		s.finalMark(param.idx, param.hb)
	}
	s.finalMarks = nil

	for _, g := range allgs {
		// scan root gc bits in case dwarf searching failure
		for i, fr := range g.frames {
			it := &(fr.gcMaskBitIterator)
			if it.nextPtr(false) != 0 {
				var idx *pprofIndex
				for j := len(g.frames) - 1; j >= i; j-- {
					idx = idx.pushHead(s.pb, g.frames[j].funcName)
				}
				s.finalMark(idx, it)
			}
		}
	}

	// final mark segment root bits
	for i, seg := range s.bss {
		it := &(seg.gcMaskBitIterator)
		if it.nextPtr(false) != 0 {
			idx := (*pprofIndex)(nil).pushHead(s.pb, fmt.Sprintf("bss segment[%d]", i))
			s.finalMark(idx, it)
		}
	}
	for i, seg := range s.data {
		it := &(seg.gcMaskBitIterator)
		if it.nextPtr(false) != 0 {
			idx := (*pprofIndex)(nil).pushHead(s.pb, fmt.Sprintf("data segment[%d]", i))
			s.finalMark(idx, it)
		}
	}

	s.pb.flush()
	log.Printf("successfully output to `%s`\n", filename)
	return s, nil
}
