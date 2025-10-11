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
