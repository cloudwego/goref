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
		Root: &MemoryNode{Children: []*MemoryNode{}},
	},
	Timeout: 30 * time.Second,
}
