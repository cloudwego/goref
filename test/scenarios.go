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

package test

import "time"

// LocalStringScenario tests local string allocation
var LocalStringScenario = TestScenario{
	Name: "local string allocation",
	Code: `package main
import (
	"fmt"
	"time"
	"runtime"
)

func main() {
	localString := new(string)
	*localString = time.Now().String()

	fmt.Println("READY")
	time.Sleep(100 * time.Second)

	go func() {
		runtime.KeepAlive(localString)
	}()
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.main.localString",
				Size:  ExactValue(80),
				Count: ExactValue(2),
			},
		},
	},
	Timeout: 30 * time.Second,
}

// LocalSliceAllocationScenario tests local slice allocation
var LocalSliceAllocationScenario = TestScenario{
	Name: "local slice allocation",
	Code: `package main
import (
	"fmt"
	"time"
	"runtime"
)
func main() {
	globalSlice := []int{1, 2, 3, 4, 5, 6}

	fmt.Println("READY")

	time.Sleep(100 * time.Second)

	go func() {
		runtime.KeepAlive(globalSlice)
	}()
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.main.globalSlice",
				Size:  ExactValue(48), // slice struct + 6 int elements
				Count: ExactValue(1),
			},
		},
	},
	Timeout: 30 * time.Second,
}

// GlobalSliceScenario tests global slice and its internal fields
var GlobalSliceScenario = TestScenario{
	Name: "global slice",
	Code: `package main
import (
	"fmt"
	"time"
)

var globalSlice []int
var globalArray *[5]int

func main() {
	// Initialize global slice in main to ensure heap allocation
	globalSlice = make([]int, 5)

	// Initialize global array pointer in main to ensure heap allocation
	globalArray = new([5]int)

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.globalSlice",
				Size:  ExactValue(48), // slice struct + 5 int elements
				Count: ExactValue(1),
			},
			{
				Name:  "main.globalArray",
				Size:  ExactValue(48), // array struct + 5 int elements
				Count: ExactValue(1),
			},
		},
	},
	Timeout: 30 * time.Second,
}

// GlobalMapScenario tests global map and its key/value internal fields
var GlobalMapScenario = TestScenario{
	Name: "global map",
	Code: `package main
import (
	"fmt"
	"time"
)

var globalMap map[string]string

func main() {
	// Initialize global map in main to ensure heap allocation
	globalMap = make(map[string]string)
	globalMap[time.Now().String()] = time.Now().String()
	globalMap[time.Now().String()] = time.Now().String()
	globalMap[time.Now().String()] = time.Now().String()

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.globalMap",
				Size:  ExactValue(336),
				Count: ExactValue(2),
				Children: []*MemoryNode{
					{
						Name:  "$mapkey",
						Type:  "string",
						Size:  ExactValue(192),
						Count: ExactValue(3),
					},
					{
						Name:  "$mapval",
						Type:  "string",
						Size:  ExactValue(192),
						Count: ExactValue(3),
					},
				},
			},
		},
	},
	Timeout: 30 * time.Second,
}

// GlobalStructScenario tests global struct and field-level references
var GlobalStructScenario = TestScenario{
	Name: "global struct",
	Code: `package main
import (
	"fmt"
	"time"
)

type Data struct {
	ID   int
	Name string
	Ptr  *int
}

var globalStruct *Data

func main() {
	// Initialize global struct in main to ensure heap allocation
	globalStruct = &Data{
		ID:   123,
		Name: "test",
	}

	// Use a scan object (contains pointer field) to avoid tiny allocator
	// instability for *int targets across architectures.
	holder := &struct {
		V    int
		Next *int
	}{
		V:    456,
		Next: nil,
	}
	globalStruct.Ptr = &holder.V

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.globalStruct",
				Size:  ExactValue(32),
				Count: ExactValue(1),
				Children: []*MemoryNode{
					{
						Name:  "Ptr",
						Size:  ExactValue(16),
						Count: ExactValue(1),
						Type:  "*int",
					},
				},
			},
		},
	},
	Timeout: 30 * time.Second,
}

// ClosureScenario tests closure variable capture and memory references
var ClosureScenario = TestScenario{
	Name: "closure variable capture",
	Code: `package main
import (
	"fmt"
	"time"
)

func main() {
	// Create a variable that will be captured by closure
	capturedValue := 42

	// Create closure that captures the variable
	globalClosure := func() {
		fmt.Printf("Captured value: %d\n", capturedValue)
	}

	fmt.Println("READY")
	time.Sleep(100 * time.Second)

	// Call the closure to prevent optimization
	go globalClosure()
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.main.globalClosure",
				Size:  ExactValue(16),
				Count: ExactValue(1),
			},
		},
	},
	Timeout: 30 * time.Second,
}

// FieldLockScenario tests field reference locking entire struct
var FieldLockScenario = TestScenario{
	Name: "field reference locking",
	Code: `package main
import (
	"fmt"
	"time"
)

type LargeStruct struct {
	ID     int
	Name   string
	Data   [100]byte
	Flag   bool
}

var fieldPtr *int

func main() {
	// Create a large struct
	localStruct := &LargeStruct{
		ID:   123,
		Name: "large struct",
	}
	for i := 0; i < 100; i++ {
		localStruct.Data[i] = byte(i)
	}

	// Create a pointer to a field, which should lock the entire struct
	fieldPtr = &localStruct.ID

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.fieldPtr",
				Size:  ExactValue(128),
				Count: ExactValue(1),
			},
		},
	},
	Timeout: 30 * time.Second,
}

// NestedStructScenario tests deeply nested struct field references
var NestedStructScenario = TestScenario{
	Name: "nested struct field references",
	Code: `package main
import (
	"fmt"
	"time"
)

type InnerData struct {
	ID   int64
	Name string
}

type MiddleStruct struct {
	Inner     *InnerData
	Count     int
	Timestamp int64
}

type OuterStruct struct {
	Middle    *MiddleStruct
	Version   string
	IsActive  bool
}

var globalOuter *OuterStruct

func main() {
	// Create deeply nested struct
	globalOuter = &OuterStruct{
		Middle: &MiddleStruct{
			Inner: &InnerData{
				ID:   12345,
				Name: "nested data",
			},
			Count:     999,
			Timestamp: time.Now().Unix(),
		},
		Version:  "v1.0.0",
		IsActive: true,
	}

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.globalOuter",
				Size:  ExactValue(32),
				Count: ExactValue(1),
				Children: []*MemoryNode{
					{
						Name:  "Middle",
						Size:  ExactValue(24),
						Count: ExactValue(1),
						Type:  "*main.MiddleStruct",
						Children: []*MemoryNode{
							{
								Name:  "Inner",
								Size:  ExactValue(24),
								Count: ExactValue(1),
								Type:  "*main.InnerData",
							},
						},
					},
				},
			},
		},
	},
	Timeout: 30 * time.Second,
}

// FinalizerScenario tests finalizer function references
var FinalizerScenario = TestScenario{
	Name: "finalizer function references",
	Code: `package main
import (
	"fmt"
	"runtime"
	"time"
)

type ToFin struct {
	data [100]int64
	next *ToFin
}

func main() {
	// Create object with finalizer
	obj := &ToFin{
		data: [100]int64{1, 2, 3, 4, 5},
	}

	// Create a separate object for finalizer to reference
	finTarget := &ToFin{
		data: [100]int64{9, 8, 7, 6, 5},
	}

	// Set finalizer that references finTarget
	runtime.SetFinalizer(obj, func(*ToFin) {
		// Reference finTarget to prevent optimization
		_ = finTarget.data[0]
		fmt.Printf("Finalizer called\n")
	})

	fmt.Println("READY")
	time.Sleep(100 * time.Second)

	// Keep objects alive
	runtime.KeepAlive(obj)
	runtime.KeepAlive(finTarget)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.main.finTarget",
				Size:  ExactValue(896),
				Count: ExactValue(1),
			},
			{
				Name:  "main.main.obj",
				Size:  ExactValue(896),
				Count: ExactValue(1),
			},
		},
	},
	Timeout: 30 * time.Second,
}

// FinalizerQueuedScenario validates objects/functions retained by finalizers that
// have been queued but not completed yet.
var FinalizerQueuedScenario = TestScenario{
	Name: "finalizer queued but not executed",
	Code: `package main
import (
	"fmt"
	"runtime"
	"time"
)

type ToFin struct {
	data [100]int64
}

func forceGC(rounds int) {
	for i := 0; i < rounds; i++ {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
}

func startBlockingFinalizer(started chan struct{}) {
	blockerObj := &ToFin{data: [100]int64{1, 2, 3, 4}}
	runtime.SetFinalizer(blockerObj, func(*ToFin) {
		close(started)
		select {}
	})
}

func enqueuePendingFinalizer() {
	target := &ToFin{data: [100]int64{9, 8, 7, 6}}
	obj := &ToFin{data: [100]int64{5, 4, 3, 2}}
	runtime.SetFinalizer(obj, func(*ToFin) {
		// Keep target alive only through finalizer closure.
		_ = target.data[0]
	})
}

func waitBlockingFinalizerStarted(started chan struct{}) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		forceGC(1)
		select {
		case <-started:
			return
		default:
			if time.Now().After(deadline) {
				panic("blocking finalizer did not start")
			}
		}
	}
}

func main() {
	started := make(chan struct{})
	startBlockingFinalizer(started)
	waitBlockingFinalizerStarted(started)

	// While the finalizer goroutine is blocked, enqueue another finalizer.
	enqueuePendingFinalizer()
	forceGC(4)

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "runtime.SetFinalizer.obj",
				Count: MinValue(1),
			},
			{
				Name:  "runtime.SetFinalizer.fn",
				Count: MinValue(1),
			},
		},
	},
	Timeout:            30 * time.Second,
	RootPrefixes:       []string{"runtime.SetFinalizer."},
	AllowExtraChildren: true,
}

// InterfaceScenario tests interface variable references
var InterfaceScenario = TestScenario{
	Name: "interface variable references",
	Code: `package main
import (
	"fmt"
	"time"
)

type Data struct {
	ID   int64
	Name string
}

type Writer interface {
	Write(data string) error
}

type FileWriter struct {
	fileData *Data
}

func (fw *FileWriter) Write(data string) error {
	// Simulate writing to file
	return nil
}

var globalWriter Writer

func main() {
	// Create interface variable and assign to global to force heap allocation
	globalWriter = &FileWriter{}

	// Create data objects
	data := &Data{
		ID:   12345,
		Name: "test data 1",
	}

	// Store interface reference to data
	globalWriter.(*FileWriter).fileData = data

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.globalWriter",
				Size:  ExactValue(8),
				Count: ExactValue(1),
				Children: []*MemoryNode{
					{
						Name:  "fileData",
						Size:  ExactValue(24),
						Count: ExactValue(1),
						Type:  "*main.Data",
					},
				},
			},
		},
	},
	Timeout: 30 * time.Second,
}

// ChannelScenario tests channel references
var ChannelScenario = TestScenario{
	Name: "channel references",
	Code: `package main
import (
	"fmt"
	"time"
)

type Message struct {
	ID   int64
	Text string
}

var globalStringChan chan string
var globalMessageChan chan *Message

func main() {
	// Create channels and assign to globals to force heap allocation
	globalStringChan = make(chan string, 10)
	globalMessageChan = make(chan *Message, 5)

	// Create message objects
	msg1 := &Message{
		ID:   1001,
		Text: "Hello",
	}
	msg2 := &Message{
		ID:   1002,
		Text: "World",
	}

	// Send messages to channels
	go func() {
		globalStringChan <- "test string"
		globalMessageChan <- msg1
		globalMessageChan <- msg2
	}()

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.globalStringChan",
				Size:  RangeValue(256, 272),
				Count: ExactValue(2),
			},
			{
				Name:  "main.globalMessageChan",
				Size:  RangeValue(144, 160),
				Count: ExactValue(2),
				Children: []*MemoryNode{
					{
						Name:  "[0]",
						Size:  ExactValue(24),
						Count: ExactValue(1),
						Type:  "*main.Message",
					},
					{
						Name:  "[1]",
						Size:  ExactValue(24),
						Count: ExactValue(1),
						Type:  "*main.Message",
					},
				},
			},
		},
	},
	Timeout: 30 * time.Second,
}

// MallocHeaderHiddenTypeScenario validates hidden-type pointer discovery via
// GC metadata fallback. On runtimes with allocation headers enabled, this also
// exercises malloc-header-based type metadata loading for large scan objects.
var MallocHeaderHiddenTypeScenario = TestScenario{
	Name: "malloc header hidden type chain",
	Code: `package main
import (
	"fmt"
	"time"
	"unsafe"
)

type Node struct {
	Value int64
	Next  *Node
}

type Hidden struct {
	Head *Node
	Pad  [560]byte
	Tail *Node
}

var hiddenHeaderRaw *byte

func buildHidden() *byte {
	n1 := &Node{Value: 101}
	n2 := &Node{Value: 202}
	n1.Next = n2

	h := &Hidden{
		Head: n1,
		Tail: n2,
	}
	return (*byte)(unsafe.Pointer(h))
}

func main() {
	hiddenHeaderRaw = buildHidden()
	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.hiddenHeaderRaw",
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

// CircularReferenceScenario tests circular reference behavior
var CircularReferenceScenario = TestScenario{
	Name: "circular reference behavior",
	Code: `package main
import (
	"fmt"
	"time"
)

type Node struct {
	ID   int
	Next *Node
}

var node1, node2 *Node

func main() {
	// Create two nodes that reference each other
	node1 = &Node{ID: 1}
	node2 = &Node{ID: 2}

	// Create simple circular reference
	node1.Next = node2
	node2.Next = node1

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.node1",
				Size:  ExactValue(16), // Node struct (int + pointer)
				Count: ExactValue(1),
				Children: []*MemoryNode{
					{
						Name:  "Next",
						Size:  ExactValue(16),
						Count: ExactValue(1),
						Type:  "*main.Node",
					},
				},
			},
		},
	},
	Timeout: 30 * time.Second,
}
