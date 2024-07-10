package main

import (
	"time"
)

type PtrStruct struct {
	a [3]int64
	c *int32
}

type BigElem struct {
	Ptrs [512]PtrStruct
	X    uint64
}

var (
	noscan     = make([]byte, 1024)
	noHeader   = make([]PtrStruct, 512/32)
	withHeader = make([]PtrStruct, 512)
	bigElem    *BigElem
	bigElem1   BigElem
	bigElem2   = &BigElem{}
	large      = make([]PtrStruct, 512*1024)
)

func main() {
	c := int32(123)
	bigElem = &BigElem{}
	bigElem.Ptrs[0].c = &c
	time.Sleep(100 * time.Second)
}
