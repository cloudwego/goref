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

import (
	"debug/dwarf"
	"unsafe"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/modern-go/reflect2"
)

/*
 * Although reluctant to do so, the unsafe operations within this file are currently necessary.
 * However, I believe in the future, we will consider managing the dependencies on delve in a more appropriate manner.
 */

var (
	memField     reflect2.StructField
	stackField   reflect2.StructField
	stackLoField reflect2.StructField
	stackHiField reflect2.StructField
	offsetField  reflect2.StructField
)

func init() {
	vt := reflect2.TypeOf(proc.Variable{}).(reflect2.StructType)
	memField = vt.FieldByName("mem")

	gt := reflect2.TypeOf(proc.G{}).(reflect2.StructType)
	stackField = gt.FieldByName("stack")

	st := reflect2.TypeOf(stackField.Get(&proc.G{})).(reflect2.PtrType).Elem().(reflect2.StructType)
	stackLoField = st.FieldByName("lo")
	stackHiField = st.FieldByName("hi")

	ft := reflect2.TypeOf(proc.Function{}).(reflect2.StructType)
	offsetField = ft.FieldByName("offset")
}

func getVariableMem(v *proc.Variable) proc.MemoryReadWriter {
	return *memField.Get(v).(*proc.MemoryReadWriter)
}

func getStack(g *proc.G) (lo, hi uint64) {
	stack := stackField.Get(g)
	lo = *stackLoField.Get(stack).(*uint64)
	hi = *stackHiField.Get(stack).(*uint64)
	return
}

func getFunctionOffset(f *proc.Function) (offset dwarf.Offset) {
	return *offsetField.Get(f).(*dwarf.Offset)
}

//go:linkname getDwarfTree github.com/go-delve/delve/pkg/proc.(*Image).getDwarfTree
func getDwarfTree(image *proc.Image, off dwarf.Offset) (*godwarf.Tree, error)

//go:linkname funcToImage github.com/go-delve/delve/pkg/proc.(*BinaryInfo).funcToImage
func funcToImage(bi *proc.BinaryInfo, fn *proc.Function) *proc.Image

//go:linkname readVarEntry github.com/go-delve/delve/pkg/proc.readVarEntry
func readVarEntry(entry *godwarf.Tree, image *proc.Image) (name string, typ godwarf.Type, err error)

//go:linkname findType github.com/go-delve/delve/pkg/proc.(*BinaryInfo).findType
func findType(bi *proc.BinaryInfo, name string) (godwarf.Type, error)

//go:linkname getModuleData github.com/go-delve/delve/pkg/proc.(*BinaryInfo).getModuleData
func getModuleData(bi *proc.BinaryInfo, mem proc.MemoryReadWriter) ([]proc.ModuleData, error)

//go:linkname newVariable github.com/go-delve/delve/pkg/proc.newVariable
func newVariable(name string, addr uint64, dwarfType godwarf.Type, bi *proc.BinaryInfo, mem proc.MemoryReadWriter) *proc.Variable

func uint64s2str(us []uint64) string {
	p := unsafe.Pointer(unsafe.SliceData(us))
	return unsafe.String((*byte)(p), len(us)*8)
}

func str2uint64s(s string) []uint64 {
	p := unsafe.Pointer(unsafe.StringData(s))
	return unsafe.Slice((*uint64)(p), len(s)/8)
}
