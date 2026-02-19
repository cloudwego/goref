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

//go:build go1.26 && goexperiment.greenteagc

package test

import "time"

func init() {
	testCases = append(testCases,
		GreenTeaUnsafeSinglePtrScenario,
		GreenTeaUnsafeMultiPtrScenario,
	)
}

// GreenTeaUnsafeSinglePtrScenario verifies fallback GC bitmap scanning when
// DWARF type info only sees *byte while the real object still has pointer fields.
var GreenTeaUnsafeSinglePtrScenario = TestScenario{
	Name: "greenteagc unsafe single pointer fallback",
	Code: `package main
import (
	"fmt"
	"time"
	"unsafe"
)

type Node struct {
	A int64
	B int64
}

// Pointer is intentionally placed at offset 8 to validate non-zero pointer slot bitmap handling.
type Hidden struct {
	Pad  [8]byte
	Next *Node
}

var hiddenRaw *byte

func buildHidden() *byte {
	n := &Node{A: 11, B: 22}
	h := &Hidden{Next: n}
	return (*byte)(unsafe.Pointer(h))
}

func main() {
	hiddenRaw = buildHidden()
	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.hiddenRaw",
				Count: ExactValue(1),
				Children: []*MemoryNode{
					{
						Name:  "$sub_objects$",
						Count: ExactValue(1),
					},
				},
			},
		},
	},
	Timeout: 30 * time.Second,
}

// GreenTeaUnsafeMultiPtrScenario verifies multiple downstream objects discovered
// via GC mask scanning when the root is intentionally type-erased to *byte.
var GreenTeaUnsafeMultiPtrScenario = TestScenario{
	Name: "greenteagc unsafe multi pointer fallback",
	Code: `package main
import (
	"fmt"
	"time"
	"unsafe"
)

type Node struct {
	A int64
	B int64
}

type Hidden struct {
	Left  *Node
	Pad   [8]byte
	Right *Node
}

var hiddenRaw *byte

func buildHidden() *byte {
	n1 := &Node{A: 101, B: 202}
	n2 := &Node{A: 303, B: 404}
	h := &Hidden{
		Left:  n1,
		Right: n2,
	}
	return (*byte)(unsafe.Pointer(h))
}

func main() {
	hiddenRaw = buildHidden()
	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.hiddenRaw",
				Count: ExactValue(1),
				Children: []*MemoryNode{
					{
						Name:  "$sub_objects$",
						Count: ExactValue(2),
					},
				},
			},
		},
	},
	Timeout: 30 * time.Second,
}
