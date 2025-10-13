package creator

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

func Create() interface{} {
	buf := make([]byte, 1024)
	str := string(buf)
	return &Request{
		A: new(int64),
		B: &str,
		C: []string{string(buf), string(buf), string(buf)},
		D: &SubRequest{
			E: map[string]string{
				string(buf): string(buf),
			},
			F: map[int64]*MyChan{
				123456: {
					cchan: make(chan *InnerMessage, 100),
				},
			},
		},
		X: []*SubRequest{
			{
				E: map[string]string{
					string(buf): string(buf),
				},
				F: map[int64]*MyChan{
					123456: {
						cchan: make(chan *InnerMessage, 100),
					},
				},
			},
		},
	}
}
