// Copyright 2025 CloudWeGo Authors
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

// Test_CeilDivide tests the CeilDivide function.
func Test_CeilDivide(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		d    int64
		want int64
	}{
		{name: "No remainder", n: 10, d: 2, want: 5},
		{name: "With remainder", n: 10, d: 3, want: 4},
		{name: "Zero numerator", n: 0, d: 3, want: 0},
		{name: "Large numbers", n: 9223372036854775807, d: 2, want: 4611686018427387904},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CeilDivide(tt.n, tt.d); got != tt.want {
				t.Errorf("CeilDivide(%d, %d) = %d, want %d", tt.n, tt.d, got, tt.want)
			}
		})
	}
}

func Test_CeilDivide2(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		d1   int64
		d2   int64
		want int64
	}{
		{
			name: "Test case 1: n is divisible by d1 and d2",
			n:    10,
			d1:   2,
			d2:   2,
			want: 3,
		},
		{
			name: "Test case 2: n is not divisible by d1 but divisible by d2",
			n:    11,
			d1:   2,
			d2:   2,
			want: 3,
		},
		{
			name: "Test case 3: n is divisible by d1 but not by d2",
			n:    10,
			d1:   2,
			d2:   3,
			want: 2,
		},
		{
			name: "Test case 4: n is not divisible by d1 and not by d2",
			n:    11,
			d1:   2,
			d2:   3,
			want: 2,
		},
		{
			name: "Test case 5: n is zero",
			n:    0,
			d1:   2,
			d2:   3,
			want: 0,
		},
		{
			name: "Test case 6: n larger than d1*d2",
			n:    9,
			d1:   8,
			d2:   1,
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CeilDivide2(tt.n, tt.d1, tt.d2)
			if got != tt.want {
				t.Errorf("CeilDivide2() = %v, want %v", got, tt.want)
			}
		})
	}
}
