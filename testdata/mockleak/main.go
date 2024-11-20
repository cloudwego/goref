package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"time"

	"github.com/cloudwego/goref/testdata/mockleak/creator"
	"github.com/cloudwego/goref/testdata/mockleak/holder"
)

func main() {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	receiver := holder.ReceiveChan()
	for i := 0; i < 100000; i++ {
		receiver <- creator.Create()
	}
	time.Sleep(1 * time.Minute)
}
