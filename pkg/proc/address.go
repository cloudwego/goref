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

// Address provides a simple encapsulation of address operations.
type Address uint64

// Sub subtracts b from a. Requires a >= b.
func (a Address) Sub(b Address) int64 {
	return int64(a - b)
}

// Add adds x to address a.
func (a Address) Add(x int64) Address {
	return a + Address(x)
}
