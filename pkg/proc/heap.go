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
	"go/constant"
	"math"
	"math/bits"

	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
)

type spanInfo struct {
	base      Address  // start address of the span
	elemSize  int64    // size of objects in the span
	spanSize  int64    // size of the span
	visitMask []uint64 // 64 * n mark bits, one for every 8 bytes
	ptrMask   []uint64 // 64 * n ptr bits, one for every 8 bytes

	spanclass     spanClass // alloc header span
	largeTypeAddr uint64    // for large type
}

// marks the pointer, return true of not marked before.
func (sp *spanInfo) mark(addr Address) bool {
	offset := addr.Sub(sp.base)
	if sp.visitMask[offset/8/64]&(1<<(offset/8%64)) != 0 {
		return false
	} else {
		sp.visitMask[offset/8/64] |= 1 << (offset / 8 % 64)
		return true
	}
}

func (sp *spanInfo) elemEnd(base Address) Address {
	end := base.Add(sp.elemSize)
	if end > sp.base.Add(sp.spanSize) {
		end = sp.base.Add(sp.spanSize)
	}
	return end
}

type segment struct {
	start, end Address
	visitMask  []uint64
}

func (s *segment) init(start, end Address) {
	s.start, s.end = start, end
	maskLen := (end - start) / 8 / 64
	if (end-start)/8%64 != 0 {
		maskLen += 1
	}
	s.visitMask = make([]uint64, maskLen)
}

func (s *segment) mark(addr Address) (success bool) {
	if addr >= s.start && addr < s.end {
		offset := addr.Sub(s.start)
		if s.visitMask[offset/8/64]&(1<<(offset/8%64)) != 0 {
			return false
		} else {
			s.visitMask[offset/8/64] |= 1 << (offset / 8 % 64)
			return true
		}
	}
	return false
}

type segments []*segment

func (ss segments) mark(addr Address) (success bool, seg *segment) {
	if len(ss) == 1 {
		// most of scene
		if ss[0].mark(addr) {
			return true, ss[0]
		}
		return false, nil
	}
	for _, seg = range ss {
		if seg.mark(addr) {
			return true, seg
		}
	}
	return false, nil
}

// stack inherits segment.
type stack struct {
	segment
}

// HeapScope contains the proc info for this round of scanning.
type HeapScope struct {
	// runtime constants
	pageSize        int64
	heapArenaBytes  int64
	pagesPerArena   int64
	arenaL1Bits     int64
	arenaL2Bits     int64
	arenaBaseOffset int64

	// data/bss segments
	data, bss segments

	// enable alloc header
	enableAllocHeader      bool
	minSizeForMallocHeader int64

	// arena info map
	arenaInfo []*[]*[]*spanInfo

	finalizers []finalizer

	mds []proc.ModuleData

	mem   proc.MemoryReadWriter
	bi    *proc.BinaryInfo
	scope *proc.EvalScope

	finalMarks []finalMarkParam
}

func (s *HeapScope) readHeap() error {
	rdr := s.bi.Images[0].DwarfReader()
	if rdr == nil {
		return errors.New("error dwarf reader is nil")
	}
	tmp, err := s.scope.EvalExpression("runtime.mheap_", loadSingleValue)
	if err != nil {
		return err
	}
	mheap := toRegion(tmp, s.bi)
	// read runtime constants
	s.pageSize = s.rtConstant("_PageSize")
	spanInUse := uint8(s.rtConstant("_MSpanInUse"))
	if spanInUse == 0 {
		spanInUse = uint8(s.rtConstant("mSpanInUse"))
	}
	s.heapArenaBytes = s.rtConstant("heapArenaBytes")
	s.pagesPerArena = s.heapArenaBytes / s.pageSize
	kindSpecialFinalizer := uint8(s.rtConstant("_KindSpecialFinalizer"))
	s.arenaBaseOffset = s.getArenaBaseOffset()
	s.arenaL1Bits, s.arenaL2Bits = s.rtConstant("arenaL1Bits"), s.rtConstant("arenaL2Bits")
	s.minSizeForMallocHeader = s.rtConstant("minSizeForMallocHeader")

	// start read all spans
	spans, spanInfos := s.readAllSpans(mheap.Field("allspans").Array(), spanInUse, kindSpecialFinalizer)

	// start read arenas
	if !s.readArenas(mheap) {
		// read typed pointers when enabled alloc header
		s.readTypePointers(spans, spanInfos)
	}

	// read firstmoduledata
	return s.readModuleData()
}

func (s *HeapScope) readAllSpans(allspans *region, spanInUse, kindSpecialFinalizer uint8) (spans []*region, spanInfos []*spanInfo) {
	// read all spans
	n := allspans.ArrayLen()
	to := &region{}
	for i := int64(0); i < n; i++ {
		allspans.ArrayIndex(i, to)
		sp := to.Deref()
		base := Address(sp.Field("startAddr").Uintptr())
		elemSize := int64(sp.Field("elemsize").Uintptr())
		spanSize := int64(sp.Field("npages").Uintptr()) * s.pageSize
		st := sp.Field("state")
		if st.IsStruct() && st.HasField("s") { // go1.14+
			st = st.Field("s")
		}
		if st.IsStruct() && st.HasField("value") { // go1.20+
			st = st.Field("value")
		}
		if st.Uint8() != spanInUse {
			continue
		}
		maskLen := spanSize / 8 / 64
		if spanSize/8%64 != 0 {
			maskLen += 1
		}
		spi := &spanInfo{
			base: base, elemSize: elemSize, spanSize: spanSize,
			visitMask: make([]uint64, maskLen), ptrMask: make([]uint64, maskLen),
		}
		max := base.Add(spanSize)
		for addr := base; addr < max; addr = addr.Add(s.pageSize) {
			s.allocSpan(addr, spi)
		}
		if err := s.addSpecial(sp, spi, kindSpecialFinalizer); err != nil {
			logflags.DebuggerLogger().Errorf("%v", err)
		}
		// for go 1.22 with allocation header
		spans = append(spans, sp)
		spanInfos = append(spanInfos, spi)
	}
	return
}

func (s *HeapScope) heapBitsInSpan(elemSize int64) bool {
	return elemSize <= s.minSizeForMallocHeader
}

func (s *HeapScope) readTypePointers(spans []*region, spanInfos []*spanInfo) {
	for i, sp := range spans {
		spi := spanInfos[i]
		spc := spanClass(sp.Field("spanclass").Uint8())
		spi.spanclass = spc
		if spc.noscan() {
			continue
		}
		if s.heapBitsInSpan(spi.elemSize) {
			bitmapSize := spi.spanSize / 8 / 8
			readUint64Array(s.mem, uint64(spi.base.Add(spi.spanSize-bitmapSize)), spi.ptrMask)
			continue
		}
		// with alloc headers
		if spc.sizeclass() == 0 {
			largeTypeAddr := sp.Field("largeType").Address()
			spi.largeTypeAddr = uint64(largeTypeAddr)
		}
	}
}

func (s *HeapScope) readArenas(mheap *region) (success bool) {
	arenaSize := s.rtConstant("heapArenaBytes")
	level1Table := mheap.Field("arenas")
	level1size := level1Table.ArrayLen()
	to := &region{}
	var readBitmapFunc func(heapArena *region, min Address)
	for level1 := int64(0); level1 < level1size; level1++ {
		level1Table.ArrayIndex(level1, to)
		if to.Address() == 0 {
			continue
		}
		level2table := to.Deref()
		level2size := level2table.ArrayLen()
		for level2 := int64(0); level2 < level2size; level2++ {
			level2table.ArrayIndex(level2, to)
			if to.Address() == 0 {
				continue
			}
			heapArena := to.Deref()
			min := Address(arenaSize*(level2+level1*level2size) - s.arenaBaseOffset)
			if readBitmapFunc == nil {
				if readBitmapFunc = s.readBitmapFunc(heapArena); readBitmapFunc == nil {
					return false
				}
			}
			readBitmapFunc(heapArena, min)
		}
	}
	return true
}

func (s *HeapScope) readBitmapFunc(heapArena *region) func(heapArena *region, min Address) {
	// read bitmap
	if heapArena.HasField("bitmap") { // Before go 1.22
		if oneBitBitmap := heapArena.HasField("noMorePtrs"); oneBitBitmap { // Starting in go 1.20
			return func(heapArena *region, min Address) {
				s.readOneBitBitmap(heapArena.Field("bitmap"), min)
			}
		} else {
			return func(heapArena *region, min Address) {
				s.readMultiBitBitmap(heapArena.Field("bitmap"), min)
			}
		}
	} else if heapArena.HasField("heapArenaPtrScalar") && heapArena.Field("heapArenaPtrScalar").HasField("bitmap") { // go 1.22 without allocation headers
		return func(heapArena *region, min Address) {
			s.readOneBitBitmap(heapArena.Field("heapArenaPtrScalar").Field("bitmap"), min)
		}
	} else { // go 1.22 with allocation headers
		s.enableAllocHeader = true
		return nil
	}
}

// base must be the base address of an object in then span
func (s *HeapScope) copyGCMask(sp *spanInfo, base Address) Address {
	if !s.enableAllocHeader {
		return base
	}
	if sp.spanclass.noscan() {
		return base
	}
	if s.heapBitsInSpan(sp.elemSize) {
		return base
	}
	if sp.spanclass.sizeclass() != 0 {
		// alloc type in header
		typeAddr, _ := readUintRaw(s.mem, uint64(base), 8)
		s.readType(sp, Address(typeAddr), base.Add(8), sp.elemEnd(base))
		return base.Add(8)
	} else {
		// large type
		s.readType(sp, Address(sp.largeTypeAddr), base, sp.elemEnd(base))
		return base
	}
}

func (s *HeapScope) readType(sp *spanInfo, typeAddr, addr, end Address) {
	var typeSize, ptrBytes int64
	var gcDataAddr Address
	mem := cacheMemory(s.mem, uint64(typeAddr), int(gcDataOffset+8))
	if typeSize_, err := readUintRaw(mem, uint64(typeAddr.Add(sizeOffset)), 8); err != nil || typeSize_ == 0 {
		return
	} else {
		typeSize = int64(typeSize_)
	}
	if ptrBytes_, err := readUintRaw(mem, uint64(typeAddr.Add(ptrBytesOffset)), 8); err != nil || ptrBytes_ == 0 {
		return
	} else {
		ptrBytes = int64(ptrBytes_)
	}
	if gcDataAddr_, err := readUintRaw(mem, uint64(typeAddr.Add(gcDataOffset)), 8); err != nil {
		return
	} else {
		gcDataAddr = Address(gcDataAddr_)
		bLen := int(math.Ceil(float64(ptrBytes)/512)) * 512
		mem = cacheMemory(s.mem, uint64(gcDataAddr), bLen/64)
	}
	elem := addr
	for {
		if addr >= elem.Add(ptrBytes) {
			// No more ptrs, copy the next element.
			// Maybe overflow beyond the real object, but doesn't affect the correctness.
			elem = elem.Add(typeSize)
			addr = elem
		}
		if addr >= end {
			break
		}
		mask, err := readUintRaw(mem, uint64(gcDataAddr.Add(addr.Sub(elem)/64)), 8)
		if err != nil {
			logflags.DebuggerLogger().Errorf("read gc data addr error: %v", err)
			break
		}
		var headBits int64
		if addr.Add(8*64) > end {
			headBits = (end.Sub(addr)) / 8
			mask &^= ((1 << (64 - headBits)) - 1) << headBits
		}
		offset := addr.Sub(sp.base)
		idx := offset / 8 / 64
		bit := offset / 8 % 64
		sp.ptrMask[idx] |= mask << bit
		if idx+1 < int64(len(sp.ptrMask)) {
			// copy remaining mask to next
			sp.ptrMask[idx+1] |= mask >> (64 - bit)
		}
		// next
		addr = addr.Add(8 * 64)
	}
}

// Read a one-bit bitmap (Go 1.20+), recording the heap pointers.
func (s *HeapScope) readOneBitBitmap(bitmap *region, min Address) {
	n := bitmap.ArrayLen()
	to := &region{}
	for i := int64(0); i < n; i++ {
		bitmap.ArrayIndex(i, to)
		m := to.Uintptr()
		var j int64
		for {
			j += int64(bits.TrailingZeros64(m >> j))
			if j >= 64 {
				break
			}
			s.setHeapPtr(min.Add((i*64 + j) * 8))
			j++
		}
	}
}

// TODO: use bitmapMask to speed up memory lookup.
// const bitmapMask uint64 = 0xf0f0f0f0f0f0f0f0

// Read a multi-bit bitmap (Go 1.11-1.20), recording the heap pointers.
func (s *HeapScope) readMultiBitBitmap(bitmap *region, min Address) {
	ptrSize := int64(s.bi.Arch.PtrSize())
	n := bitmap.ArrayLen()
	to := &region{}
	for i := int64(0); i < n; i++ {
		// batch read 8 bytes, which corresponds to 32 pointers.
		bitmap.ArrayIndex(i, to)
		m := to.Uint8()
		for j := int64(0); j < 4; j++ {
			if m>>uint(j)&1 != 0 {
				s.setHeapPtr(min.Add((i*4 + j) * ptrSize))
			}
		}
	}
}

func (s *HeapScope) indexes(addr Address) (l1, l2, idx uint) {
	if s.arenaL1Bits == 0 {
		l2 = s.arenaIndex(uintptr(addr))
	} else {
		ri := s.arenaIndex(uintptr(addr))
		l1 = ri >> s.arenaL2Bits
		l2 = ri & (1<<s.arenaL2Bits - 1)
	}
	idx = (uint(addr) / uint(s.pageSize)) % uint(s.pagesPerArena)
	return
}

func (s *HeapScope) allocSpan(addr Address, sp *spanInfo) {
	l1, l2, idx := s.indexes(addr)
	if len(s.arenaInfo) == 0 {
		s.arenaInfo = make([]*[]*[]*spanInfo, 1<<s.arenaL1Bits)
	}
	if l1 >= uint(len(s.arenaInfo)) {
		return
	}
	l1Info := s.arenaInfo[l1]
	if l1Info == nil {
		tmp := make([]*[]*spanInfo, 1<<s.arenaL2Bits)
		l1Info = &tmp
		s.arenaInfo[l1] = l1Info
	}
	if l2 >= uint(len(*l1Info)) {
		return
	}
	arena := (*l1Info)[l2]
	if arena == nil {
		tmp := make([]*spanInfo, s.pagesPerArena)
		arena = &tmp
		(*s.arenaInfo[l1])[l2] = arena
	}
	if idx >= uint(len(*arena)) {
		return
	}
	if (*arena)[idx] == nil {
		(*arena)[idx] = sp
	}
}

func (s *HeapScope) findSpanAndBase(addr Address) (sp *spanInfo, base Address) {
	sp = s.spanOf(addr)
	if sp == nil {
		return
	}
	offset := addr.Sub(sp.base)
	base = sp.base.Add(offset / sp.elemSize * sp.elemSize)
	return
}

func (s *HeapScope) setHeapPtr(a Address) {
	sp := s.spanOf(a)
	if sp == nil {
		return
	}
	offset := a.Sub(sp.base)
	sp.ptrMask[offset/8/64] |= uint64(1) << (offset / 8 % 64)
}

type heapBits struct {
	base Address   // heap base
	addr Address   // iterator address
	end  Address   // cannot reach end
	sp   *spanInfo // span info
}

func newHeapBits(base, end Address, sp *spanInfo) *heapBits {
	return &heapBits{base: base, addr: base, end: end, sp: sp}
}

// To avoid traversing fields/elements that escape the actual valid scope.
// e.g. (*[1 << 16]scase)(unsafe.Pointer(cas0)) in runtime.selectgo.
var errOutOfRange = errors.New("out of heap span range")

// resetGCMask will reset ptrMask corresponding to the address,
// which will never be marked again by the finalMark.
func (hb *heapBits) resetGCMask(addr Address) error {
	if hb == nil {
		return nil
	}
	if addr < hb.base || addr >= hb.end {
		return errOutOfRange
	}
	// TODO: check gc mask
	offset := addr.Sub(hb.sp.base)
	hb.sp.ptrMask[offset/8/64] &= ^(1 << (offset / 8 % 64))
	return nil
}

// nextPtr returns next ptr address starts from 'addr', returns 0 if not found.
// If ack == true, the 'addr' will automatically increment to the next
// starting address to be searched.
func (hb *heapBits) nextPtr(ack bool) Address {
	if hb == nil {
		return 0
	}
	startOffset, endOffset := hb.addr.Sub(hb.sp.base), hb.end.Sub(hb.sp.base)
	if startOffset >= endOffset || startOffset < 0 || endOffset > hb.sp.spanSize {
		return 0
	}
	for startOffset < endOffset {
		ptrIdx := startOffset / 8 / 64
		i := startOffset / 8 % 64
		j := int64(bits.TrailingZeros64(hb.sp.ptrMask[ptrIdx] >> i))
		if j == 64 {
			// search the next ptr
			startOffset = (ptrIdx + 1) * 64 * 8
			continue
		}
		addr := hb.sp.base.Add(startOffset + j*8)
		if addr >= hb.end {
			return 0
		}
		if ack {
			hb.addr = addr.Add(8)
		}
		return addr
	}
	return 0
}

func (s *HeapScope) spanOf(addr Address) *spanInfo {
	l1, l2, idx := s.indexes(addr)
	if l1 < uint(len(s.arenaInfo)) {
		l1Info := s.arenaInfo[l1]
		if l1Info != nil && l2 < uint(len(*l1Info)) {
			l2Info := (*l1Info)[l2]
			if l2Info != nil && idx < uint(len(*l2Info)) {
				return (*l2Info)[idx]
			}
		}
	}
	return nil
}

func (s *HeapScope) arenaIndex(p uintptr) uint {
	return uint((p + uintptr(s.arenaBaseOffset)) / uintptr(s.heapArenaBytes))
}

func (s *HeapScope) readModuleData() error {
	tmp, err := s.scope.EvalExpression("runtime.firstmoduledata", loadSingleValue)
	if err != nil {
		return err
	}
	firstmoduledata := toRegion(tmp, s.bi)
	for md := firstmoduledata; md.a != 0; md = md.Field("next").Deref() {
		var data, bss segment
		data.init(Address(md.Field("data").Uintptr()), Address(md.Field("edata").Uintptr()))
		bss.init(Address(md.Field("bss").Uintptr()), Address(md.Field("ebss").Uintptr()))
		s.data = append(s.data, &data)
		s.bss = append(s.bss, &bss)
	}
	return nil
}

type finalizer struct {
	p  Address // finalized pointer
	fn Address // finalizer function, always 8 bytes
}

func (s *HeapScope) addSpecial(sp *region, spi *spanInfo, kindSpecialFinalizer uint8) error {
	// Process special records.
	spty, _ := findType(s.bi, "runtime.specialfinalizer")
	for special := sp.Field("specials"); special.Address() != 0; special = special.Field("next") {
		special = special.Deref() // *special to special
		if special.Field("kind").Uint8() != kindSpecialFinalizer {
			// All other specials (just profile records) can't point into the heap.
			continue
		}
		var fin finalizer
		p := spi.base.Add(int64(special.Field("offset").Uint16()) / spi.elemSize * spi.elemSize)
		fin.p = p
		spf := *special
		spf.typ = spty
		fin.fn = spf.Field("fn").a
		s.finalizers = append(s.finalizers, fin)
	}
	return nil
}

func (s *HeapScope) getArenaBaseOffset() int64 {
	x, _ := s.scope.EvalExpression("runtime.arenaBaseOffsetUintptr", loadSingleValue)
	// arenaBaseOffset changed sign in 1.15. Callers treat this
	// value as it was specified in 1.14, so we negate it here.
	xv, _ := constant.Int64Val(x.Value)
	return -xv
}

func (s *HeapScope) rtConstant(name string) int64 {
	x, _ := s.scope.EvalExpression("runtime."+name, loadSingleValue)
	if x != nil {
		v, _ := constant.Int64Val(x.Value)
		return v
	}
	return 0
}
