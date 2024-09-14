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
	"reflect"
	"unsafe"
)

// A spanClass represents the size class and noscan-ness of a span.
type spanClass uint8

func (sc spanClass) noscan() bool {
	return sc&1 != 0
}

func (sc spanClass) sizeclass() int8 {
	return int8(sc >> 1)
}

type (
	TFlag   uint8
	NameOff int32
	TypeOff int32
)

// Type copied from go/src/internal/abi/type.go.
type Type struct {
	Size_       uintptr
	PtrBytes    uintptr
	Hash        uint32
	TFlag       TFlag
	Align_      uint8
	FieldAlign_ uint8
	Kind_       uint8
	Equal       func(unsafe.Pointer, unsafe.Pointer) bool
	GCData      *byte
	Str         NameOff
	PtrToThis   TypeOff
}

var sizeOffset, ptrBytesOffset, gcDataOffset int64

func init() {
	rtype := reflect.TypeOf(Type{})
	sf, _ := rtype.FieldByName("Size_")
	sizeOffset = int64(sf.Offset)
	sf, _ = rtype.FieldByName("PtrBytes")
	ptrBytesOffset = int64(sf.Offset)
	sf, _ = rtype.FieldByName("GCData")
	gcDataOffset = int64(sf.Offset)
}
