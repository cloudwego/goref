// Copyright 2024 CloudWeGo Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package proc

import (
	"fmt"
	"math"
	"sort"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
)

func (s *HeapScope) readFuncTab(md *region, _funcTyp godwarf.Type) {
	pcln := md.Field("pclntable")
	pctab := md.Field("pctab")
	ftab := md.Field("ftab")
	n := ftab.SliceLen() - 1 // last slot is a dummy, just holds entry
	for i := int64(0); i < n; i++ {
		ft := ftab.SliceIndex(i)
		var entry Address
		var funcoff int64
		if ft.HasField("entryoff") {
			entry = s.textAddr(md, ft.Field("entryoff").Uint32())
			funcoff = int64(ft.Field("funcoff").Uint32())
		} else {
			// Prior to 1.18, functab.entry directly referenced the
			// entries.
			entry = Address(ft.Field("entry").Uintptr())
			// funcoff changed type, but had the same meaning.
			funcoff = int64(ft.Field("funcoff").Uintptr())
		}
		fun := s.bi.PCToFunc(uint64(entry))
		if fun == nil {
			continue
		}
		f := pcln.SliceIndex(funcoff).Cast(_funcTyp) // runtime._func
		funcdata, stackMap, err := s.readFunc(md, f, pctab)
		if err != nil {
			logflags.DebuggerLogger().Errorf("readFuncTab readFunc err: %v", err)
			return
		}
		fe := s.funcExtraMap[fun]
		fe.funcdata = funcdata
		fe.stackMap = stackMap
		s.funcExtraMap[fun] = fe
	}
}

// readFunc parses a runtime._func and returns a *Func.
// r must have type runtime._func.
// pcln must have type []byte and represent the module's pcln table region.
func (s *HeapScope) readFunc(md, f, pctab *region) (funcdata []Address, stackMap pcTab, err error) {
	// Parse pcdata and funcdata, which are laid out beyond the end of the _func.
	nfd := f.Field("nfuncdata")
	nfdv := uint32(nfd.Uint8())
	a := nfd.a.Add(nfd.typ.Size())

	var pcdata []uint32
	for i := uint32(0); i < f.Field("npcdata").Uint32(); i++ {
		var val uint64
		val, err = readUintRaw(s.mem, uint64(a), 4)
		if err != nil {
			return
		}
		pcdata = append(pcdata, uint32(val))
		a = a.Add(4)
	}

	is118OrGreater := md.HasField("gofunc")
	if !is118OrGreater {
		// Since 1.18, funcdata no longer needs to be aligned.
		a = a.Align(8)
	}
	goFuncPtr := md.Field("gofunc").Uintptr()

	for i := uint32(0); i < nfdv; i++ {
		if is118OrGreater {
			// Since 1.18, funcdata contains offsets from go.func.*.
			var off uint64
			off, err = readUintRaw(s.mem, uint64(a), 4)
			if err != nil {
				return
			}
			if uint32(off) == ^uint32(0) {
				// No entry.
				funcdata = append(funcdata, 0)
			} else {
				funcdata = append(funcdata, Address(goFuncPtr+off))
			}
			a = a.Add(4)
		} else {
			// Prior to 1.18, funcdata contains pointers directly
			// to the data.
			var fd uint64
			fd, err = readUintRaw(s.mem, uint64(a), 8)
			if err != nil {
				return
			}
			funcdata = append(funcdata, Address(fd))
			a = a.Add(8)
		}
	}

	// Read pcln tables we need.
	if len(pcdata) > PCDATA_StackMapIndex {
		s.readPCTab(&stackMap, pctab.SliceIndex(int64(pcdata[PCDATA_StackMapIndex])).a)
	} else {
		stackMap.setEmpty()
	}
	return
}

// textAddr returns the address of a text offset.
//
// Equivalent to runtime.moduledata.textAddr.
func (s *HeapScope) textAddr(md *region, off32 uint32) Address {
	if s.textAddrCache == nil {
		s.textAddrCache = &textCache{}

		s.textAddrCache.text = uintptr(md.Field("text").Uintptr())

		textsectmap := md.Field("textsectmap")
		length := textsectmap.SliceLen()
		s.textAddrCache.textsectmap = make([]textsect, length)
		for i := int64(0); i < length; i++ {
			sect := textsectmap.SliceIndex(i)
			s.textAddrCache.textsectmap[i] = textsect{
				vaddr:    uintptr(sect.Field("vaddr").Uintptr()),
				end:      uintptr(sect.Field("end").Uintptr()),
				baseaddr: uintptr(sect.Field("baseaddr").Uintptr()),
			}
		}
	}
	off := uintptr(off32)
	res := s.textAddrCache.text + off

	length := int64(len(s.textAddrCache.textsectmap))
	if length > 1 {
		for i := int64(0); i < length; i++ {
			sect := s.textAddrCache.textsectmap[i]

			if off >= sect.vaddr && off < sect.end || (i == length-1 && off == sect.end) {
				res = sect.baseaddr + off - sect.vaddr
			}
		}
	}

	return Address(res)
}

type textsect struct {
	vaddr    uintptr // prelinked section vaddr
	end      uintptr // vaddr + section length
	baseaddr uintptr // relocated section address
}

type textCache struct {
	text        uintptr
	textsectmap []textsect
}

// a pcTab maps from an offset in a function to an int64.
type pcTab struct {
	entries []pcTabEntry
}

type pcTabEntry struct {
	bytes int64 // # of bytes this entry covers
	val   int64 // value over that range of bytes
}

// readPCTab parses a pctab from the core file at address data.
func (s *HeapScope) readPCTab(tb *pcTab, data Address) {
	var pcQuantum int64
	switch s.bi.Arch.Name {
	case "386", "amd64", "amd64p32":
		pcQuantum = 1
	case "s390x":
		pcQuantum = 2
	case "arm", "arm64", "mips", "mipsle", "mips64", "mips64le", "ppc64", "ppc64le":
		pcQuantum = 4
	default:
		panic("unknown architecture " + s.bi.Arch.Name)
	}
	val := int64(-1)
	first := true
	for {
		// Advance value.
		v, n := readVarint(s.mem, uint64(data))
		if v == 0 && !first {
			return
		}
		data = data.Add(n)
		if v&1 != 0 {
			val += ^(v >> 1)
		} else {
			val += v >> 1
		}

		// Advance pc.
		v, n = readVarint(s.mem, uint64(data))
		data = data.Add(n)
		tb.entries = append(tb.entries, pcTabEntry{bytes: v * pcQuantum, val: val})
		first = false
	}
}

func (t *pcTab) setEmpty() {
	t.entries = []pcTabEntry{{bytes: math.MaxInt64, val: -1}}
}

// sort.Search ?
func (t *pcTab) find(off int64) (int64, error) {
	for _, e := range t.entries {
		if off < e.bytes {
			return e.val, nil
		}
		off -= e.bytes
	}
	return 0, fmt.Errorf("can't find pctab entry for offset %#x", off)
}

func (s *HeapScope) stackPtrMask(frames []proc.Stackframe) []framePointerMask {
	stkmapTyp, err := findType(s.bi, "runtime.stackmap")
	if err != nil {
		logflags.DebuggerLogger().Errorf("stackPtrMask findType `runtime.stackmap` err: %v", err)
		return nil
	}
	stkObjRecordTyp, err := findType(s.bi, "runtime.stackObjectRecord")
	if err != nil {
		logflags.DebuggerLogger().Errorf("stackPtrMask findType `runtime.stackObjectRecord` err: %v", err)
		return nil
	}
	var frPtrMasks []framePointerMask
	for i := range frames {
		pc := frames[i].Regs.PC()
		fn := s.bi.PCToFunc(pc)
		if fn == nil {
			continue
		}
		sp := Address(frames[i].Regs.SP())
		fp := Address(frames[i].Regs.FrameBase)
		off := int64(pc) - int64(fn.Entry)
		fne := s.funcExtraMap[fn]
		if fne.funcdata == nil {
			continue
		}
		// locals and args
		for _, pm := range []int{FUNCDATA_LocalsPointerMaps, FUNCDATA_ArgsPointerMaps} {
			if len(fne.funcdata) > pm {
				addr := fne.funcdata[pm]
				if addr != 0 {
					vars := &region{mem: s.mem, bi: s.bi, a: addr, typ: stkmapTyp}
					n := vars.Field("n").Int32()       // # of bitmaps
					nbit := vars.Field("nbit").Int32() // # of bits per bitmap
					if nbit == 0 {
						continue
					}
					idx, err := fne.stackMap.find(off)
					if err != nil {
						logflags.DebuggerLogger().Errorf("cannot read stack map at pc=%#x: %v", pc, err)
						continue
					}
					if idx < 0 {
						idx = 0
					}
					if idx < int64(n) {
						bits := vars.Field("bytedata").a.Add(int64(nbit+7) / 8 * idx)
						var base Address
						if pm == FUNCDATA_LocalsPointerMaps {
							base = s.localsOffset(fp, sp, nbit)
						} else {
							base = s.argsOffset(fp)
						}
						if base == 0 {
							// not support stack pointer mask for this architecture
							return nil
						}
						ptrMask := make([]uint64, CeilDivide(int64(nbit), 64))
						data := make([]byte, CeilDivide(int64(nbit), 8))
						_, err = s.mem.ReadMemory(data, uint64(bits))
						if err != nil {
							logflags.DebuggerLogger().Errorf("cannot read bytedata at pc=%#x: %v", pc, err)
							continue
						}
						for i, mask := range data {
							// convert to 64-bit mask
							ptrMask[i/8] |= uint64(mask) << (8 * (i % 8))
						}
						frPtrMasks = append(frPtrMasks, framePointerMask{
							funcName:          fn.Name,
							gcMaskBitIterator: *newGCBitsIterator(base, base.Add(int64(nbit)*8), base, ptrMask),
						})
					}
				}
			}
		}
		// stack vars
		if len(fne.funcdata) > FUNCDATA_StackObjects {
			addr := fne.funcdata[FUNCDATA_StackObjects]
			if addr != 0 {
				n, err := readUintRaw(s.mem, uint64(addr), 8)
				if err != nil {
					logflags.DebuggerLogger().Errorf("cannot read stack objects at pc=%#x: %v", pc, err)
					continue
				}
				addr = addr.Add(8)
				vars := &region{mem: s.mem, bi: s.bi, a: addr, typ: fakeArrayType(n, stkObjRecordTyp)}
				var to region
				for i := int64(0); i < int64(n); i++ {
					vars.ArrayIndex(i, &to)
					off := to.Field("off").Int32()
					// ???
				}
			}
		}
	}
	sort.Slice(frPtrMasks, func(i, j int) bool {
		return frPtrMasks[i].base < frPtrMasks[j].base
	})
	return frPtrMasks
}

func (s *HeapScope) localsOffset(fp, sp Address, nbit int32) Address {
	// see internal/goarch/goarch.go
	var minFrameSize int
	switch s.bi.Arch.Name {
	case "amd64":
		minFrameSize = 0
	case "arm64":
		minFrameSize = 8
	default:
		return 0
	}
	// see runtime/traceback.go
	if !(minFrameSize > 0) {
		// On x86, call instruction pushes return PC before entering new function.
		fp -= 8
	}
	if fp > sp {
		fp -= 8
	}
	fp = fp.Add(-int64(nbit) * 8)
	return fp
}

func (s *HeapScope) argsOffset(fp Address) Address {
	// see internal/goarch/goarch.go
	var minFrameSize int64
	switch s.bi.Arch.Name {
	case "amd64":
		minFrameSize = 0
	case "arm64":
		minFrameSize = 8
	default:
		return 0
	}
	// see runtime/traceback.go
	return fp.Add(minFrameSize)
}

// readVarint reads a varint from the mem.
// val is the value, n is the number of bytes consumed.
func readVarint(mem proc.MemoryReadWriter, a uint64) (val, n int64) {
	for {
		v, err := readUintRaw(mem, a, 1)
		if err != nil {
			return 0, 0
		}
		b := byte(v)
		val |= int64(b&0x7f) << uint(n*7)
		n++
		a++
		if b&0x80 == 0 {
			return
		}
	}
}
