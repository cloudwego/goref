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

import "testing"

func TestHeapBits(t *testing.T) {
	hb := newGCBitsIterator(0, 1024, 0, make([]uint64, 2))
	// set 16, 72, 208, 504, 928 as pointer
	offsets := []int64{16, 72, 208, 504, 928}
	for _, offset := range offsets {
		hb.mask[offset/8/64] |= 1 << (offset / 8 % 64)
	}
	for i, offset := range offsets {
		var nextOffset int64
		if i < len(offsets)-1 {
			nextOffset = offsets[i+1]
		}
		if hb.nextPtr(false) != Address(offset) {
			t.Fatalf("not %d", offset)
		}
		hb.resetGCMask(Address(offset))
		if hb.nextPtr(false) != Address(nextOffset) {
			t.Fatalf("not %d", nextOffset)
		}
	}
}
