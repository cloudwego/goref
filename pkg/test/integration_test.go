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

import (
	"testing"
)

var testCases = []TestScenario{
	LocalStringScenario,
	LocalSliceAllocationScenario,
	GlobalSliceScenario,
	GlobalMapScenario,
	GlobalStructScenario,
	ClosureScenario,
	FieldLockScenario,
	NestedStructScenario,
	FinalizerScenario,
	CleanupScenario,
	InterfaceScenario,
	ChannelScenario,
	AllocationHeaderScenario,
	CircularReferenceScenario,
}

// TestScenarios runs individual test scenarios using table-driven approach
func TestScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Define test cases using table-driven approach

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			framework := NewTestFramework(t)
			framework.AddScenario(tc)
			framework.RunAll()
		})
	}
}
