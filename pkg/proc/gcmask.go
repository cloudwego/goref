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
	"math/bits"
)

type gcMaskBitIterator struct {
	base, end Address // cannot reach end

	maskBase Address
	mask     []uint64

	addr Address // iterator address
}

// nextPtr returns next ptr address starts from 'addr', returns 0 if not found.
// If ack == true, the 'addr' will automatically increment to the next
// starting address to be searched.
func (b *gcMaskBitIterator) nextPtr(ack bool) Address {
	if b == nil {
		return 0
	}
	startOffset, endOffset := b.addr.Sub(b.maskBase), b.end.Sub(b.maskBase)
	if startOffset >= endOffset || startOffset < 0 {
		return 0
	}
	for startOffset < endOffset {
		ptrIdx := startOffset / 8 / 64
		i := startOffset / 8 % 64
		j := int64(bits.TrailingZeros64(b.mask[ptrIdx] >> i))
		if j == 64 {
			// search the next ptr
			startOffset = (ptrIdx + 1) * 64 * 8
			continue
		}
		addr := b.maskBase.Add(startOffset + j*8)
		if addr >= b.end {
			return 0
		}
		if ack {
			b.addr = addr.Add(8)
		}
		return addr
	}
	return 0
}

// resetGCMask will reset ptrMask corresponding to the address,
// which will never be marked again by the finalMark.
func (b *gcMaskBitIterator) resetGCMask(addr Address) error {
	if b == nil {
		return nil
	}
	if addr < b.base || addr >= b.end {
		return errOutOfRange
	}
	// TODO: check gc mask
	offset := addr.Sub(b.maskBase)
	b.mask[offset/8/64+1] &= ^(1 << (offset / 8 % 64))
	return nil
}

func newGCBitsIterator(base, end, maskBase Address, ptrMask []uint64) *gcMaskBitIterator {
	return &gcMaskBitIterator{base: base, end: end, mask: ptrMask, addr: base, maskBase: maskBase}
}

// To avoid traversing fields/elements that escape the actual valid scope.
// e.g. (*[1 << 16]scase)(unsafe.Pointer(cas0)) in runtime.selectgo.
var errOutOfRange = errors.New("out of heap span range")
