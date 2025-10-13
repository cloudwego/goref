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

package test

import "time"

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
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.main.globalSlice",
				Size:  48, // slice struct + 6 int elements
				Count: 1,
			},
		}},
	},
	Timeout: 30 * time.Second,
}

// GlobalBasicTypesScenario tests global basic types using pointers to ensure heap allocation
var GlobalBasicTypesScenario = TestScenario{
	Name: "global basic types",
	Code: `package main
import (
	"fmt"
	"time"
)

var (
	globalInt    *int64
	globalString *string
	globalFloat  *float64
)

func main() {
	// Initialize global pointers in main to ensure heap allocation via mallocgc
	globalInt = new(int64)
	*globalInt = 12345

	globalString = new(string)
	*globalString = "hello world"

	globalFloat = new(float64)
	*globalFloat = 3.14159

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalString",
				Size:  16,
				Count: 1,
			},
			{
				Name:  "main.globalInt",
				Size:  16,
				Count: 1,
			},
		}},
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

func main() {
	// Initialize global slice in main to ensure heap allocation
	globalSlice = make([]int, 5)
	for i := 0; i < 5; i++ {
		globalSlice[i] = i * 10
	}

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalSlice",
				Size:  48,
				Count: 1,
			},
		}},
	},
	Timeout: 30 * time.Second,
}

// GlobalArrayScenario tests global array and its elements
var GlobalArrayScenario = TestScenario{
	Name: "global array",
	Code: `package main
import (
	"fmt"
	"time"
)

var globalArray *[5]int

func main() {
	// Initialize global array pointer in main to ensure heap allocation
	globalArray = new([5]int)
	globalArray[0] = 10
	globalArray[1] = 20
	globalArray[2] = 30
	globalArray[3] = 40
	globalArray[4] = 50

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalArray",
				Size:  48,
				Count: 1,
			},
		}},
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

var globalMap map[string]int

func main() {
	// Initialize global map in main to ensure heap allocation
	globalMap = make(map[string]int)
	globalMap["apple"] = 10
	globalMap["banana"] = 20
	globalMap["cherry"] = 30

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalMap",
				Size:  256,
				Count: 2,
			},
		}},
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
	globalStruct.Ptr = new(int)
	*globalStruct.Ptr = 456

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalStruct",
				Size:  32,
				Count: 1,
			},
		}},
	},
	Timeout: 30 * time.Second,
}

// StructFieldScenario tests struct field-level references
var StructFieldScenario = TestScenario{
	Name: "struct field references",
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

type Container struct {
	Data *Data
	Num  int
}

var globalContainer *Container
var globalIntPtr *int

func main() {
	// Create nested struct with field references
	globalContainer = &Container{
		Data: &Data{
			ID:   123,
			Name: "test",
		},
		Num: 456,
	}

	// Create separate int pointer
	globalIntPtr = new(int)
	*globalIntPtr = 789

	// Point struct field to the int pointer
	globalContainer.Data.Ptr = globalIntPtr

	fmt.Println("READY")
	time.Sleep(100 * time.Second)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalContainer",
				Size:  16,
				Count: 1,
			},
		}},
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

var globalClosure func()

func main() {
	// Create a variable that will be captured by closure
	capturedValue := 42

	// Create closure that captures the variable
	globalClosure = func() {
		fmt.Printf("Captured value: %d\n", capturedValue)
	}

	fmt.Println("READY")
	time.Sleep(100 * time.Second)

	// Call the closure to prevent optimization
	globalClosure()
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalClosure",
				Size:  16,
				Count: 1,
			},
		}},
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

var globalStruct *LargeStruct
var fieldPtr *int

func main() {
	// Create a large struct
	globalStruct = &LargeStruct{
		ID:   123,
		Name: "large struct",
	}
	for i := 0; i < 100; i++ {
		globalStruct.Data[i] = byte(i)
	}

	// Create a pointer to a field, which should lock the entire struct
	fieldPtr = &globalStruct.ID

	fmt.Println("READY")
	time.Sleep(100 * time.Second)

	// Use the field pointer to prevent optimization
	*fieldPtr = 456
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalStruct",
				Size:  128,
				Count: 1,
			},
		}},
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
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalOuter",
				Size:  32,
				Count: 1,
			},
		}},
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
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.main.finTarget",
				Size:  896,
				Count: 1,
			},
			{
				Name:  "main.main.obj",
				Size:  896,
				Count: 1,
			},
		}},
	},
	Timeout: 30 * time.Second,
}

// CleanupScenario tests cleanup function references
var CleanupScenario = TestScenario{
	Name: "cleanup function references",
	Code: `package main
import (
	"fmt"
	"runtime"
	"time"
)

type ToCleanup struct {
	data [50]int64
}

func cleanupFunc(arg *ToCleanup) {
	fmt.Printf("Cleanup called: %d\n", arg.data[0])
}

func main() {
	// Create object that will have cleanup
	obj := &ToCleanup{
		data: [50]int64{10, 20, 30, 40, 50},
	}

	// Create cleanup target
	cleanupTarget := &ToCleanup{
		data: [50]int64{50, 40, 30, 20, 10},
	}

	// Add cleanup function
	runtime.AddCleanup(obj, cleanupFunc, cleanupTarget)

	fmt.Println("READY")
	time.Sleep(100 * time.Second)

	// Keep objects alive
	runtime.KeepAlive(obj)
	runtime.KeepAlive(cleanupTarget)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.main.obj",
				Size:  416,
				Count: 1,
			},
			{
				Name:  "main.main.cleanupTarget",
				Size:  416,
				Count: 1,
			},
		}},
	},
	Timeout: 30 * time.Second,
}

// InterfaceScenario tests interface variable references
var InterfaceScenario = TestScenario{
	Name: "interface variable references",
	Code: `package main
import (
	"fmt"
	"runtime"
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
	filePath string
}

func (fw *FileWriter) Write(data string) error {
	// Simulate writing to file
	return nil
}

var globalWriter Writer

func main() {
	// Create interface variable and assign to global to force heap allocation
	globalWriter = &FileWriter{
		filePath: "/tmp/test.txt",
	}

	// Create data objects
	data1 := &Data{
		ID:   12345,
		Name: "test data 1",
	}
	data2 := &Data{
		ID:   67890,
		Name: "test data 2",
	}

	// Store interface reference to data
	globalWriter.(*FileWriter).filePath = data1.Name
	globalWriter.(*FileWriter).filePath = data2.Name

	fmt.Println("READY")
	time.Sleep(100 * time.Second)

	// Keep objects alive
	runtime.KeepAlive(globalWriter)
	runtime.KeepAlive(data1)
	runtime.KeepAlive(data2)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalWriter",
				Size:  16,
				Count: 1,
			},
		}},
	},
	Timeout: 30 * time.Second,
}

// ChannelScenario tests channel references
var ChannelScenario = TestScenario{
	Name: "channel references",
	Code: `package main
import (
	"fmt"
	"runtime"
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

	// Keep objects alive
	runtime.KeepAlive(globalStringChan)
	runtime.KeepAlive(globalMessageChan)
	runtime.KeepAlive(msg1)
	runtime.KeepAlive(msg2)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.globalStringChan",
				Size:  272,
				Count: 2,
			},
			{
				Name:  "main.globalMessageChan",
				Size: 160,
				Count: 2,
			},
		}},
	},
	Timeout: 30 * time.Second,
}

// AllocationHeaderScenario tests allocation header behavior with different object sizes
var AllocationHeaderScenario = TestScenario{
	Name: "allocation header behavior",
	Code: `package main
import (
	"fmt"
	"runtime"
	"time"
)

var (
	smallObj  *int64      // 8 bytes - no malloc header
	mediumObj *[16]int64  // 128 bytes - should have malloc header
	largeObj  *[100]int64 // 800 bytes - should have malloc header
)

func main() {
	// Small object (8 bytes) - typically no malloc header
	smallObj = new(int64)
	*smallObj = 12345

	// Medium object (128 bytes) - should include malloc header
	mediumObj = new([16]int64)
	for i := 0; i < 16; i++ {
		mediumObj[i] = int64(i * 10)
	}

	// Large object (800 bytes) - should include malloc header
	largeObj = new([100]int64)
	for i := 0; i < 100; i++ {
		largeObj[i] = int64(i)
	}

	fmt.Println("READY")
	time.Sleep(100 * time.Second)

	// Keep objects alive
	runtime.KeepAlive(smallObj)
	runtime.KeepAlive(mediumObj)
	runtime.KeepAlive(largeObj)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.smallObj",
				Size:  16, // 8 bytes data + 8 bytes overhead
				Count: 1,
			},
			{
				Name:  "main.mediumObj",
				Size:  128, // 128 bytes, exact size match
				Count: 1,
			},
			{
				Name:  "main.largeObj",
				Size:  896, // 800 bytes data + 96 bytes overhead
				Count: 1,
			},
		}},
	},
	Timeout: 30 * time.Second,
}

// CircularReferenceScenario tests circular reference behavior
var CircularReferenceScenario = TestScenario{
	Name: "circular reference behavior",
	Code: `package main
import (
	"fmt"
	"runtime"
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

	// Keep objects alive
	runtime.KeepAlive(node1)
	runtime.KeepAlive(node2)
}
`,
	Expected: &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{
			{
				Name:  "main.node1",
				Size:  16, // Node struct (int + pointer)
				Count: 1,
			},
		}},
	},
	Timeout: 30 * time.Second,
}
