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

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-delve/delve/service/debugger"

	gorefproc "github.com/cloudwego/goref/pkg/proc"
)

// TestScenario defines a complete test scenario
type TestScenario struct {
	Name     string
	Code     string
	Expected *MemoryTree
	Timeout  time.Duration
}

// TestFramework manages integration test execution
type TestFramework struct {
	t         *testing.T
	scenarios []TestScenario
	tempDir   string
}

// NewTestFramework creates a new test framework instance
func NewTestFramework(t *testing.T) *TestFramework {
	tempDir := t.TempDir()
	return &TestFramework{
		t:       t,
		tempDir: tempDir,
	}
}

// AddScenario adds a test scenario to the framework
func (tf *TestFramework) AddScenario(scenario TestScenario) {
	tf.scenarios = append(tf.scenarios, scenario)
}

// RunAll runs all registered test scenarios
func (tf *TestFramework) RunAll() {
	for _, scenario := range tf.scenarios {
		tf.t.Run(scenario.Name, func(t *testing.T) {
			tf.runScenario(scenario)
		})
	}
}

// runScenario executes a single test scenario
func (tf *TestFramework) runScenario(scenario TestScenario) {
	tf.t.Logf("Running scenario: %s", scenario.Name)

	// Create and start test program
	program, err := tf.createTestProgram(scenario)
	if err != nil {
		tf.t.Fatalf("Failed to create test program: %v", err)
	}
	defer program.Stop()

	if err := program.Start(); err != nil {
		tf.t.Fatalf("Failed to start test program: %v", err)
	}

	if err := program.WaitForReady(); err != nil {
		tf.t.Fatalf("Test program not ready: %v", err)
	}

	outputFile := tf.tempDir + "/" + scenario.Name + ".out"
	scope, err := tf.attachAndAnalyze(program.GetPID(), outputFile, program.Binary)
	if err != nil {
		tf.t.Fatalf("Failed to attach and analyze: %v", err)
	}

	if err := tf.validateResults(scope, scenario.Expected); err != nil {
		tf.t.Errorf("Memory tree validation failed: %v", err)
	}
}

// TestProgram represents a test program instance
type TestProgram struct {
	Name      string
	Binary    string
	Cmd       *exec.Cmd
	ReadyChan chan struct{}
}

// createTestProgram creates a test program from scenario
func (tf *TestFramework) createTestProgram(scenario TestScenario) (*TestProgram, error) {
	sourceFile := filepath.Join(tf.tempDir, fmt.Sprintf("%s.go", scenario.Name))
	if err := os.WriteFile(sourceFile, []byte(scenario.Code), 0644); err != nil {
		return nil, fmt.Errorf("failed to write source file: %w", err)
	}

	// Compile program
	binaryFile := filepath.Join(tf.tempDir, scenario.Name)
	cmd := exec.Command("go", "build", "-gcflags", "all=-N -l", "-o", binaryFile, sourceFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("compilation failed: %w\nOutput: %s", err, output)
	}

	return &TestProgram{
		Name:      scenario.Name,
		Binary:    binaryFile,
		ReadyChan: make(chan struct{}),
	}, nil
}

// attachAndAnalyze attaches to the target process and analyzes references
func (tf *TestFramework) attachAndAnalyze(pid int, outputFile, binary string) (*gorefproc.ObjRefScope, error) {
	tf.t.Logf("Attaching to PID %d", pid)

	// Create debugger config
	dConf := debugger.Config{
		AttachPid:             pid,
		Backend:               "default",
		DebugInfoDirectories:  []string{},
		AttachWaitFor:         "",
		AttachWaitForInterval: 1,
		AttachWaitForDuration: 0,
	}

	// Create debugger
	dbg, err := debugger.New(&dConf, []string{binary})
	if err != nil {
		return nil, fmt.Errorf("failed to create debugger: %w", err)
	}
	defer dbg.Detach(false)

	target := dbg.Target()

	// Run reference analysis and get the scope for validation
	scope, err := gorefproc.ObjectReference(target, outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze references: %w", err)
	}

	return scope, nil
}

// validateResults validates the analysis results against expectations using memory tree comparison
func (tf *TestFramework) validateResults(scope *gorefproc.ObjRefScope, expectedTree *MemoryTree) error {
	tf.t.Logf("Analysis results:")

	// Get the real profile data from goref
	nodes, stringTable, _ := scope.GetProfileDataForTest()

	tf.t.Logf("  Raw profile data:")
	tf.t.Logf("    Nodes: %d", len(nodes))
	tf.t.Logf("    Strings: %d", len(stringTable))

	// Build memory tree from actual data
	nodeInterfaces := make(map[string]ProfileNodeInterface)
	for k, v := range nodes {
		nodeInterfaces[k] = ProfileNodeInterface(v)
	}
	actualTree := tf.buildMemoryTreeFromNodes(nodeInterfaces, stringTable)

	// Compare trees
	if err := tf.compareMemoryTrees(expectedTree, actualTree); err != nil {
		return fmt.Errorf("tree comparison failed: %v", err)
	}

	tf.t.Logf("  ✓ Memory tree validation passed")
	return nil
}

// compareMemoryTrees compares expected and actual memory trees recursively
func (tf *TestFramework) compareMemoryTrees(expected, actual *MemoryTree) error {
	return tf.compareNodes(expected.Root, actual.Root, "")
}

// compareNodes recursively compares two memory nodes
func (tf *TestFramework) compareNodes(expected, actual *MemoryNode, path string) error {
	currentPath := path
	if expected.Name != "" {
		if currentPath != "" {
			currentPath += "."
		}
		currentPath += expected.Name
	}

	// Compare basic properties
	if expected.Size != actual.Size {
		tf.t.Logf("  ✗ Size mismatch for %s: expected %d, actual %d", currentPath, expected.Size, actual.Size)
		return fmt.Errorf("size mismatch for %s", currentPath)
	}

	if expected.Count != actual.Count {
		tf.t.Logf("  ✗ Count mismatch for %s: expected %d, actual %d", currentPath, expected.Count, actual.Count)
		return fmt.Errorf("count mismatch for %s", currentPath)
	}

	// Compare children
	expectedChildren := make(map[string]*MemoryNode)
	actualChildren := make(map[string]*MemoryNode)

	for _, child := range expected.Children {
		expectedChildren[child.Name] = child
	}

	for _, child := range actual.Children {
		actualChildren[child.Name] = child
	}

	// Check for missing children
	for name, expectedChild := range expectedChildren {
		actualChild, found := actualChildren[name]
		if !found {
			tf.t.Logf("  ✗ Missing child node: %s.%s", currentPath, name)
			return fmt.Errorf("missing child node: %s.%s", currentPath, name)
		}

		// Recursively compare child nodes
		if err := tf.compareNodes(expectedChild, actualChild, currentPath); err != nil {
			return err
		}
	}

	// Check for unexpected children
	for name := range actualChildren {
		if _, found := expectedChildren[name]; !found {
			tf.t.Logf("  ⚠ Unexpected child node: %s.%s", currentPath, name)
			// Don't fail the test for unexpected nodes, just warn
		}
	}

	return nil
}

// ProfileNodeInterface defines the interface for accessing profile node data
type ProfileNodeInterface interface {
	GetCount() int64
	GetSize() int64
}

// MemoryNode represents a node in the memory reference tree
type MemoryNode struct {
	Path     string        `json:"-"`
	Name     string        `json:"name,omitempty"`     // Node name (e.g., "main.globalSlice", "main.globalSlice[0]")
	Type     string        `json:"type,omitempty"`     // Type information (e.g., "[]int", "*main.Data")
	Size     int64         `json:"size,omitempty"`     // Memory size in bytes
	Count    int64         `json:"count,omitempty"`    // Number of objects
	Children []*MemoryNode `json:"children,omitempty"` // Child nodes
}

// MemoryTree represents the complete memory reference tree
type MemoryTree struct {
	Root *MemoryNode // Root node
}

// buildMemoryTreeFromNodes builds a complete memory reference tree from goref profile nodes
func (tf *TestFramework) buildMemoryTreeFromNodes(nodes map[string]ProfileNodeInterface, stringTable []string) *MemoryTree {
	tree := &MemoryTree{
		Root: &MemoryNode{Children: []*MemoryNode{}},
	}

	mainPackageNodes := 0

	// Process each node to build the tree structure
	for key, node := range nodes {
		nodePath := tf.extractNodePathFromKey(key, stringTable)
		if nodePath == nil {
			continue // Skip empty paths
		}

		// Only include nodes from main package or system nodes for debugging
		if strings.HasPrefix(nodePath[len(nodePath)-1], "main.") {
			mainPackageNodes++
			tf.createOrUpdateNode(tree.Root, nodePath, node.GetCount(), node.GetSize())
		}
	}

	tf.t.Logf("  Found %d main package nodes", mainPackageNodes)

	return tree
}

// extractNodePathFromKey extracts a readable node path from the profile key
func (tf *TestFramework) extractNodePathFromKey(key string, stringTable []string) []string {
	// Convert the binary key back to uint64 indexes
	indexes := gorefproc.Str2uint64s(key)

	if len(indexes) == 0 {
		return nil
	}

	// Build the path from string table
	var pathParts []string
	for _, idx := range indexes {
		if int(idx) < len(stringTable) {
			str := stringTable[idx]
			if str != "" && !strings.HasPrefix(str, "inuse_") {
				pathParts = append(pathParts, str)
			}
		}
	}

	return pathParts
}

// createOrUpdateNode creates or updates a node in the memory tree
func (tf *TestFramework) createOrUpdateNode(node *MemoryNode, path []string, count, size int64) {
	if len(path) == 0 {
		node.Size += size
		node.Count += count
		return
	}

	curPath := path[len(path)-1]
	for _, child := range node.Children {
		if child.Path == curPath {
			tf.createOrUpdateNode(child, path[:len(path)-1], count, size)
			return
		}
	}
	name, typ := tf.extractNameAndTypeFromPath(curPath)
	// Create new node
	child := &MemoryNode{
		Path: curPath,
		Name: name,
		Type: typ,
	}

	node.Children = append(node.Children, child)
	tf.createOrUpdateNode(child, path[:len(path)-1], count, size)
}

// extractTypeFromKey extracts name and type information from the key path
func (tf *TestFramework) extractNameAndTypeFromPath(path string) (string, string) {
	// Simple type extraction - can be enhanced later
	parts := strings.Split(path, " ")
	if len(parts) > 1 {
		return parts[0][:len(parts[0])-1], parts[1][1 : len(parts[1])-1]
	} else {
		return parts[0], ""
	}
}

// Start starts the test program
func (tp *TestProgram) Start() error {
	tp.Cmd = exec.Command(tp.Binary)

	// Create pipes for stdout/stderr
	stdoutPipe, err := tp.Cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := tp.Cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start output readers
	go tp.readOutput("stdout", stdoutPipe)
	go tp.readOutput("stderr", stderrPipe)

	if err := tp.Cmd.Start(); err != nil {
		return fmt.Errorf("failed to start program: %w", err)
	}

	return nil
}

// Stop stops the test program
func (tp *TestProgram) Stop() error {
	if tp.Cmd != nil && tp.Cmd.Process != nil {
		tp.Cmd.Process.Kill()
		tp.Cmd.Wait()
	}
	return nil
}

// WaitForReady waits for the program to be ready for attach
func (tp *TestProgram) WaitForReady() error {
	timeout := time.After(10 * time.Second)

	for {
		select {
		case <-tp.ReadyChan:
			return nil
		case <-timeout:
			return fmt.Errorf("timeout waiting for program to be ready")
		case <-time.After(100 * time.Millisecond):
			if tp.Cmd.ProcessState != nil && tp.Cmd.ProcessState.Exited() {
				return fmt.Errorf("program exited unexpectedly")
			}
		}
	}
}

// GetPID returns the process ID
func (tp *TestProgram) GetPID() int {
	if tp.Cmd != nil && tp.Cmd.Process != nil {
		return tp.Cmd.Process.Pid
	}
	return 0
}

// readOutput reads program output and looks for READY signal
func (tp *TestProgram) readOutput(prefix string, pipe io.ReadCloser) {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[%s:%s] %s\n", tp.Name, prefix, line)

		if strings.TrimSpace(line) == "READY" {
			select {
			case <-tp.ReadyChan:
				// Already closed
			default:
				close(tp.ReadyChan)
			}
		}
	}
}
