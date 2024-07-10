package main

import (
	"context"
	"encoding/json"
	"runtime"
	"time"
	"unsafe"
)

type InnerMessage struct {
	msgs []string
}

type MyChan struct {
	cchan chan *InnerMessage
}

type SubRequest struct {
	E map[string]string
	F map[int64]*MyChan
}

type Request struct {
	A *int64
	B *string
	C []string
	D *SubRequest
	X []*SubRequest
}

func (*Request) String() string {
	return ""
}

type ReqE interface {
	String() string
}

var (
	globalReq = &Request{}
	globalCC  = make([]string, 1024)
)

//go:noinline
func escape(req *Request, str interface{}, reqI interface{}, reqE ReqE, bbbb *[2112313131]Request) {
	_, _ = json.Marshal(req)
	_, _ = json.Marshal(str)
	_, _ = json.Marshal(reqI)
	_, _ = json.Marshal(reqE)
	println(bbbb)
}

type Pointer[T any] struct {
	// Mention *T in a field to disallow conversion between Pointer types.
	// See go.dev/issue/56603 for more details.
	// Use *T, not T, to avoid spurious recursive type definition errors.
	xx [0]*T

	v unsafe.Pointer
}

// SliceByteToString converts []byte to string without copy.
// DO NOT USE unless you know what you're doing.
func SliceByteToString(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

func genericString() string {
	ss := make([]byte, 1024)
	ss = ss[100:200]
	return SliceByteToString(ss)
}

func finalizing() {
	toFin := [1000]*int64{}
	toFin2 := [1000]*int64{}
	runtime.SetFinalizer(&toFin, func(*[1000]*int64) {
		println(toFin2[0])
	})
}

func incall(a *int64, b *string) (res *Request) {
	globalReq.C = []string{genericString(), genericString(), genericString()}
	req := &Request{
		A: a,
		B: b,
		C: []string{genericString(), genericString(), genericString()},
		D: &SubRequest{
			E: map[string]string{
				genericString(): genericString(),
			},
			F: map[int64]*MyChan{
				23131: {
					cchan: make(chan *InnerMessage, 100),
				},
			},
		},
		X: []*SubRequest{
			{
				E: map[string]string{
					genericString(): genericString(),
				},
				F: map[int64]*MyChan{
					23131: {
						cchan: make(chan *InnerMessage, 100),
					},
				},
			},
		},
	}
	req.D.F[23131].cchan <- &InnerMessage{
		msgs: []string{genericString(), genericString(), genericString()},
	}
	req.X[0].F[23131].cchan <- &InnerMessage{
		msgs: []string{genericString(), genericString(), genericString()},
	}

	reqq := &req.C

	reqqq := &Request{
		A: a,
		B: b,
		C: []string{genericString(), genericString(), genericString()},
		D: &SubRequest{
			E: map[string]string{
				genericString(): genericString(),
			},
			F: map[int64]*MyChan{
				23131: {
					cchan: make(chan *InnerMessage, 100),
				},
			},
		},
		X: []*SubRequest{
			{
				E: map[string]string{
					genericString(): genericString(),
				},
				F: map[int64]*MyChan{
					23131: {
						cchan: make(chan *InnerMessage, 100),
					},
				},
			},
		},
	}
	req.D.F[23131].cchan <- &InnerMessage{
		msgs: []string{genericString(), genericString(), genericString()},
	}
	req.X[0].F[23131].cchan <- &InnerMessage{
		msgs: []string{genericString(), genericString(), genericString()},
	}

	ireq := &Request{
		A: a,
	}

	var reqI interface{} = &Request{
		A: a,
	}
	var reqE ReqE = &Request{
		A: a,
	}

	ss := make([]byte, 1024)

	sss := string(ss)

	str := func() *string {
		return &sss
	}

	println(req.X[0].E[genericString()])

	println((*reqq)[0])

	next := func() {
		println(a)
		println(b)
	}
	ctx := context.Background()
	nnext := func() {
		next()
		println(ctx.Err())
	}

	// test g stack range
	bbbb := (*[2112313131]Request)(unsafe.Pointer(&aaa))

	finalizing()

	time.Sleep(100 * time.Second)

	go func() { nnext() }()

	_ = reqqq
	_ = bbbb

	runtime.KeepAlive(req)
	runtime.KeepAlive(ireq)
	escape(req, str, reqI, reqE, bbbb)

	_ = reqI

	res = &Request{}
	return
}

var (
	// test bss range
	aaa int
	bbb = (*[2112313131]Request)(unsafe.Pointer(&aaa))
)

func main() {
	a := int64(12313)
	b := genericString()
	incall(&a, &b)
}
