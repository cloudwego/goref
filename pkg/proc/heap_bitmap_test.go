// Copyright 2026 CloudWeGo Authors
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

func TestSmallSpanBitmapBase(t *testing.T) {
	spanBase := Address(0x1000)
	spanSize := int64(0x1000)
	inlineSize := int64(128)
	span := &spanInfo{base: spanBase, spanSize: spanSize}
	bitmapSize := spanSize / 8 / 8
	legacyBase := spanBase.Add(spanSize - bitmapSize)
	tests := []struct {
		name            string
		greenTeaEnabled bool
		hasInlineBitmap bool
		inlineMarkSize  int64
		want            Address
	}{
		{
			name:            "legacy layout when greentea disabled",
			greenTeaEnabled: false,
			hasInlineBitmap: true,
			inlineMarkSize:  inlineSize,
			want:            legacyBase,
		},
		{
			name:            "green tea inline mark bits shifts bitmap base",
			greenTeaEnabled: true,
			hasInlineBitmap: true,
			inlineMarkSize:  inlineSize,
			want:            legacyBase.Add(-inlineSize),
		},
		{
			name:            "inline mark bits with zero inline size fallback",
			greenTeaEnabled: true,
			hasInlineBitmap: true,
			inlineMarkSize:  0,
			want:            legacyBase,
		},
		{
			name:            "greentea enabled without inline flag uses legacy base",
			greenTeaEnabled: true,
			hasInlineBitmap: false,
			inlineMarkSize:  inlineSize,
			want:            legacyBase,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scope := &HeapScope{
				pageSize:               spanSize,
				greenTeaGCEnabled:      tc.greenTeaEnabled,
				spanInlineMarkBitsSize: tc.inlineMarkSize,
			}
			if tc.hasInlineBitmap {
				scope.inlineMarkSpanPages = map[Address]struct{}{spanBase: {}}
			}
			got := smallSpanBitmapBase(scope, span)
			if got != tc.want {
				t.Fatalf("smallSpanBitmapBase = %#x, want %#x", uint64(got), uint64(tc.want))
			}
		})
	}
}
