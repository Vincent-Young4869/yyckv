package kv

// a key-value store backed by raft
type Kvstore struct {
	data map[string]string // current committed key-value pairs
}

func NewKVStore(errorC <-chan error) *Kvstore {
	s := &Kvstore{
		data: make(map[string]string),
	}
	return s
}
