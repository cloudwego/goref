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
	"reflect"
	"unsafe"
	_ "unsafe"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	. "github.com/go-delve/delve/pkg/proc"
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
	vt := reflect2.TypeOf(Variable{}).(reflect2.StructType)
	memField = vt.FieldByName("mem")

	gt := reflect2.TypeOf(G{}).(reflect2.StructType)
	stackField = gt.FieldByName("stack")

	st := reflect2.TypeOf(stackField.Get(&G{})).(reflect2.PtrType).Elem().(reflect2.StructType)
	stackLoField = st.FieldByName("lo")
	stackHiField = st.FieldByName("hi")

	ft := reflect2.TypeOf(Function{}).(reflect2.StructType)
	offsetField = ft.FieldByName("offset")
}

func getVariableMem(v *Variable) MemoryReadWriter {
	return *memField.Get(v).(*MemoryReadWriter)
}

func getStack(g *G) (lo, hi uint64) {
	stack := stackField.Get(g)
	lo = *stackLoField.Get(stack).(*uint64)
	hi = *stackHiField.Get(stack).(*uint64)
	return
}

func getFunctionOffset(f *Function) (offset dwarf.Offset) {
	return *offsetField.Get(f).(*dwarf.Offset)
}

/*
type functionExtra struct {
	// closureStructType is the cached struct type for closures for this function
	closureStructType *godwarf.StructType

	// rangeParent is set when this function is a range-over-func body closure
	// and points to the function that the closure was generated from.
	rangeParent *Function
	// rangeBodies is the list of range-over-func body closures for this
	// function. Only one between rangeParent and rangeBodies should be set at
	// any given time.
	rangeBodies []*Function
}

// Not support closure type before go1.23. TODO: support go1.23
//
//go:linkname extra github.com/go-delve/delve/pkg/proc.(*Function).extra
func extra(f *Function, bi *BinaryInfo) (e *functionExtra)
*/

//go:linkname image github.com/go-delve/delve/pkg/proc.(*EvalScope).image
func image(scope *EvalScope) *Image

//go:linkname getDwarfTree github.com/go-delve/delve/pkg/proc.(*Image).getDwarfTree
func getDwarfTree(image *Image, off dwarf.Offset) (*godwarf.Tree, error)

//go:linkname findType github.com/go-delve/delve/pkg/proc.(*BinaryInfo).findType
func findType(bi *BinaryInfo, name string) (godwarf.Type, error)

//go:linkname rangeParentName github.com/go-delve/delve/pkg/proc.(*Function).rangeParentName
func rangeParentName(fn *Function) string

//go:linkname readVarEntry github.com/go-delve/delve/pkg/proc.readVarEntry
func readVarEntry(entry *godwarf.Tree, image *Image) (name string, typ godwarf.Type, err error)

//go:linkname newVariable github.com/go-delve/delve/pkg/proc.newVariable
func newVariable(name string, addr uint64, dwarfType godwarf.Type, bi *BinaryInfo, mem MemoryReadWriter) *Variable

func uint64s2str(us []uint64) (s string) {
	p := unsafe.Pointer((*reflect.SliceHeader)(unsafe.Pointer(&us)).Data)
	hdr := (*reflect.StringHeader)(unsafe.Pointer(&s))
	hdr.Data = uintptr(p)
	hdr.Len = len(us) * 8
	return
}

func str2uint64s(s string) (us []uint64) {
	p := unsafe.Pointer((*reflect.StringHeader)(unsafe.Pointer(&s)).Data)
	hdr := (*reflect.SliceHeader)(unsafe.Pointer(&us))
	hdr.Data = uintptr(p)
	hdr.Len, hdr.Cap = len(s)/8, len(s)/8
	return
}
