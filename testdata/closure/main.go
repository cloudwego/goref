package main

import (
	"sync"
	"time"
)

type ctx struct {
	a []byte
	b *int64
	c *string
}

func main() {
	cf := getFunc()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Second)
		cf()
	}()
	wg.Wait()
}

func getFunc() func() {
	a := make([]byte, 1024)
	b := int64(123)
	c := string(a)
	myctx := &ctx{
		a: a,
		b: &b,
		c: &c,
	}
	return func() {
		println(myctx)
	}
}
