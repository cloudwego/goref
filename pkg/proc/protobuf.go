// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//
// Code forked from https://github.com/golang/go/blob/go1.20.14/src/runtime/pprof/protobuf.go
//
// This file has been modified by CloudWeGo authors. All CloudWeGo
// Modifications are Copyright 2024 CloudWeGo Authors.

package proc

import (
	"compress/gzip"
	"io"
)

// A protobuf is a simple protocol buffer encoder.
type protobuf struct {
	data []byte
	tmp  [16]byte
	nest int
}

func (b *protobuf) varint(x uint64) {
	for x >= 128 {
		b.data = append(b.data, byte(x)|0x80)
		x >>= 7
	}
	b.data = append(b.data, byte(x))
}

func (b *protobuf) length(tag, len int) {
	b.varint(uint64(tag)<<3 | 2)
	b.varint(uint64(len))
}

func (b *protobuf) uint64(tag int, x uint64) {
	// append varint to b.data
	b.varint(uint64(tag) << 3)
	b.varint(x)
}

func (b *protobuf) uint64s(tag int, x []uint64) {
	if len(x) > 2 {
		// Use packed encoding
		n1 := len(b.data)
		for _, u := range x {
			b.varint(u)
		}
		n2 := len(b.data)
		b.length(tag, n2-n1)
		n3 := len(b.data)
		copy(b.tmp[:], b.data[n2:n3])
		copy(b.data[n1+(n3-n2):], b.data[n1:n2])
		copy(b.data[n1:], b.tmp[:n3-n2])
		return
	}
	for _, u := range x {
		b.uint64(tag, u)
	}
}

func (b *protobuf) uint64Opt(tag int, x uint64) {
	if x == 0 {
		return
	}
	b.uint64(tag, x)
}

func (b *protobuf) int64(tag int, x int64) {
	u := uint64(x)
	b.uint64(tag, u)
}

func (b *protobuf) int64Opt(tag int, x int64) {
	if x == 0 {
		return
	}
	b.int64(tag, x)
}

func (b *protobuf) int64s(tag int, x []int64) {
	if len(x) > 2 {
		// Use packed encoding
		n1 := len(b.data)
		for _, u := range x {
			b.varint(uint64(u))
		}
		n2 := len(b.data)
		b.length(tag, n2-n1)
		n3 := len(b.data)
		copy(b.tmp[:], b.data[n2:n3])
		copy(b.data[n1+(n3-n2):], b.data[n1:n2])
		copy(b.data[n1:], b.tmp[:n3-n2])
		return
	}
	for _, u := range x {
		b.int64(tag, u)
	}
}

func (b *protobuf) string(tag int, x string) {
	b.length(tag, len(x))
	b.data = append(b.data, x...)
}

func (b *protobuf) strings(tag int, x []string) {
	for _, s := range x {
		b.string(tag, s)
	}
}

//func (b *protobuf) stringOpt(tag int, x string) {
//	if x == "" {
//		return
//	}
//	b.string(tag, x)
//}

func (b *protobuf) bool(tag int, x bool) {
	if x {
		b.uint64(tag, 1)
	} else {
		b.uint64(tag, 0)
	}
}

//func (b *protobuf) boolOpt(tag int, x bool) {
//	if !x {
//		return
//	}
//	b.bool(tag, x)
//}

type msgOffset int

func (b *protobuf) startMessage() msgOffset {
	b.nest++
	return msgOffset(len(b.data))
}

func (b *protobuf) endMessage(tag int, start msgOffset) {
	n1 := int(start)
	n2 := len(b.data)
	b.length(tag, n2-n1)
	n3 := len(b.data)
	copy(b.tmp[:], b.data[n2:n3])
	copy(b.data[n1+(n3-n2):], b.data[n1:n2])
	copy(b.data[n1:], b.tmp[:n3-n2])
	b.nest--
}

const (
	// message Profile
	tagProfile_SampleType        = 1  // repeated ValueType
	tagProfile_Sample            = 2  // repeated Sample
	tagProfile_Mapping           = 3  // repeated Mapping
	tagProfile_Location          = 4  // repeated Location
	tagProfile_Function          = 5  // repeated Function
	tagProfile_StringTable       = 6  // repeated string
	tagProfile_DropFrames        = 7  // int64 (string table index)
	tagProfile_KeepFrames        = 8  // int64 (string table index)
	tagProfile_TimeNanos         = 9  // int64
	tagProfile_DurationNanos     = 10 // int64
	tagProfile_PeriodType        = 11 // ValueType (really optional string???)
	tagProfile_Period            = 12 // int64
	tagProfile_Comment           = 13 // repeated int64
	tagProfile_DefaultSampleType = 14 // int64

	// message ValueType
	tagValueType_Type = 1 // int64 (string table index)
	tagValueType_Unit = 2 // int64 (string table index)

	// message Sample
	tagSample_Location = 1 // repeated uint64
	tagSample_Value    = 2 // repeated int64
	tagSample_Label    = 3 // repeated Label

	// message Label
	// tagLabel_Key = 1 // int64 (string table index)
	// tagLabel_Str = 2 // int64 (string table index)
	// tagLabel_Num = 3 // int64

	// message Mapping
	tagMapping_ID              = 1  // uint64
	tagMapping_Start           = 2  // uint64
	tagMapping_Limit           = 3  // uint64
	tagMapping_Offset          = 4  // uint64
	tagMapping_Filename        = 5  // int64 (string table index)
	tagMapping_BuildID         = 6  // int64 (string table index)
	tagMapping_HasFunctions    = 7  // bool
	tagMapping_HasFilenames    = 8  // bool
	tagMapping_HasLineNumbers  = 9  // bool
	tagMapping_HasInlineFrames = 10 // bool

	// message Location
	tagLocation_ID        = 1 // uint64
	tagLocation_MappingID = 2 // uint64
	tagLocation_Address   = 3 // uint64
	tagLocation_Line      = 4 // repeated Line

	// message Line
	tagLine_FunctionID = 1 // uint64
	tagLine_Line       = 2 // int64

	// message Function
	tagFunction_ID         = 1 // uint64
	tagFunction_Name       = 2 // int64 (string table index)
	tagFunction_SystemName = 3 // int64 (string table index)
	tagFunction_Filename   = 4 // int64 (string table index)
	tagFunction_StartLine  = 5 // int64
)

// A profileBuilder writes a profile incrementally from a
// stream of profile samples delivered by the runtime.
type profileBuilder struct {
	w  io.Writer
	zw *gzip.Writer

	pb        protobuf
	strings   []string
	stringMap map[string]int

	// key: indexes, val: *profileNode
	nodes map[string]*profileNode
}

type profileNode struct {
	count int64
	size  int64
}

// newProfileBuilder returns a new profileBuilder.
// CPU profiling data obtained from the runtime can be added
// by calling b.addCPUData, and then the eventual profile
// can be obtained by calling b.finish.
func newProfileBuilder(w io.Writer) *profileBuilder {
	zw, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
	b := &profileBuilder{
		w:         w,
		zw:        zw,
		strings:   []string{""},
		stringMap: map[string]int{"": 0},
		nodes:     make(map[string]*profileNode),
	}
	b.pbValueType(tagProfile_SampleType, "inuse_objects", "count")
	b.pbValueType(tagProfile_SampleType, "inuse_space", "bytes")
	return b
}

// pbLine encodes a Line message to b.pb.
func (b *profileBuilder) pbLine(tag int, funcID uint64, line int64) {
	start := b.pb.startMessage()
	b.pb.uint64Opt(tagLine_FunctionID, funcID)
	b.pb.int64Opt(tagLine_Line, line)
	b.pb.endMessage(tag, start)
}

// pbValueType encodes a ValueType message to b.pb.
func (b *profileBuilder) pbValueType(tag int, typ, unit string) {
	start := b.pb.startMessage()
	b.pb.int64(tagValueType_Type, b.stringIndex(typ))
	b.pb.int64(tagValueType_Unit, b.stringIndex(unit))
	b.pb.endMessage(tag, start)
}

// stringIndex adds s to the string table if not already present
// and returns the index of s in the string table.
func (b *profileBuilder) stringIndex(s string) int64 {
	id, ok := b.stringMap[s]
	if !ok {
		id = len(b.strings)
		b.strings = append(b.strings, s)
		b.stringMap[s] = id
	}
	return int64(id)
}

func (b *profileBuilder) addReference(indexes []uint64, count, bytes int64) {
	k := uint64s2str(indexes)
	var node *profileNode
	if node = b.nodes[k]; node == nil {
		node = &profileNode{}
		b.nodes[k] = node
	}
	node.count += count
	node.size += bytes
}

func (b *profileBuilder) flushReference() {
	for k, node := range b.nodes {
		indexes := str2uint64s(k)
		start := b.pb.startMessage()
		b.pb.int64s(tagSample_Value, []int64{node.count, node.size})
		b.pb.uint64s(tagSample_Location, indexes)
		b.pb.endMessage(tagProfile_Sample, start)
	}
}

func (b *profileBuilder) pbMapping(tag int, id, base, limit, offset uint64, file, buildID string, hasFuncs bool) {
	start := b.pb.startMessage()
	b.pb.uint64Opt(tagMapping_ID, id)
	b.pb.uint64Opt(tagMapping_Start, base)
	b.pb.uint64Opt(tagMapping_Limit, limit)
	b.pb.uint64Opt(tagMapping_Offset, offset)
	b.pb.int64Opt(tagMapping_Filename, b.stringIndex(file))
	b.pb.int64Opt(tagMapping_BuildID, b.stringIndex(buildID))
	b.pb.bool(tagMapping_HasFunctions, hasFuncs)
	b.pb.endMessage(tag, start)
}

func (b *profileBuilder) flush() {
	b.flushReference()
	for i := uint64(5); i < uint64(len(b.strings)); i++ {
		// write location
		start := b.pb.startMessage()
		b.pb.uint64Opt(tagLocation_ID, i)
		b.pbLine(tagLocation_Line, i, 0)
		b.pb.endMessage(tagProfile_Location, start)

		// write function
		start = b.pb.startMessage()
		b.pb.uint64Opt(tagFunction_ID, i)
		b.pb.int64Opt(tagFunction_Name, int64(i))
		b.pb.endMessage(tagProfile_Function, start)
	}
	// just avoid error msg from pprof tool
	b.pbMapping(tagProfile_Mapping, uint64(1), uint64(0), uint64(0xff), 0, "-", "", false)
	b.pb.strings(tagProfile_StringTable, b.strings)
	b.zw.Write(b.pb.data)
	b.zw.Close()
}

type pprofIndex struct {
	idx   uint64
	prev  *pprofIndex
	depth int
}

func (i *pprofIndex) pushHead(pb *profileBuilder, name string) *pprofIndex {
	if name == "" {
		return i
	}
	idx := uint64(pb.stringIndex(name))
	pi := &pprofIndex{
		prev: i,
		idx:  idx,
	}
	if i == nil {
		pi.depth = 0
	} else {
		pi.depth = i.depth + 1
	}
	return pi
}

func (i *pprofIndex) indexes() (res []uint64) {
	tmp := i
	for tmp != nil {
		res = append(res, tmp.idx)
		tmp = tmp.prev
	}
	return
}
