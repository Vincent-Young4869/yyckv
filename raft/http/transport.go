package http

type Transporter interface {
	Start() error
	Stop()
}

type Transport struct {
}

func (t *Transport) Start() error {
	// TODO
	return nil
}

func (t *Transport) Stop() {
	// TODO
}
