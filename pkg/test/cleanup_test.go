//go:build go1.24

package test

import "time"

func init() {
	testCases = append(testCases, CleanupScenario)
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
	Expected: &MemoryNode{
		Children: []*MemoryNode{
			{
				Name:  "main.main.obj",
				Size:  ExactValue(416),
				Count: ExactValue(1),
			},
			{
				Name:  "main.main.cleanupTarget",
				Size:  ExactValue(416),
				Count: ExactValue(1),
			},
		},
	},
	Timeout: 30 * time.Second,
}
