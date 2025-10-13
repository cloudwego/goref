package holder

func ReceiveChan() chan interface{} {
	ch := make(chan interface{}, 100)
	go func() {
		var cached []interface{}
		for v := range ch {
			cached = append(cached, v)
		}
	}()
	return ch
}
