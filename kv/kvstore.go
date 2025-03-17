package kv

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"log"
	"strings"
)

// a key-value store backed by raft
type Kvstore struct {
	proposeC    chan<- string
	data        map[string]string // current committed key-value pairs
	snapshotter *snap.Snapshotter
}

type kv struct {
	Key string
	Val string
}

func NewKVStore(snapshotter *snap.Snapshotter, proposeC chan<- string,
	commitC <-chan *commit, errorC <-chan error) *Kvstore {
	s := &Kvstore{
		proposeC:    proposeC,
		data:        make(map[string]string),
		snapshotter: snapshotter,
	}
	go s.readCommits(commitC, errorC)
	return s
}

func (s *Kvstore) Lookup(key string) (string, bool) {
	v, ok := s.data[key]
	return v, ok
}

func (s *Kvstore) Propose(k string, v string) {
	var buf strings.Builder
	if err := gob.NewEncoder(&buf).Encode(kv{k, v}); err != nil {
		log.Fatal(err)
	}
	s.proposeC <- buf.String()
}

func (s *Kvstore) readCommits(commitC <-chan *commit, errorC <-chan error) {
	for commit := range commitC {
		if commit == nil {
			// signaled to load snapshot
			//snapshot, err := s.loadSnapshot()
			//if err != nil {
			//	log.Panic(err)
			//}
			//if snapshot != nil {
			//	log.Printf("loading snapshot at term %d and index %d", snapshot.Metadata.Term, snapshot.Metadata.Index)
			//	if err := s.recoverFromSnapshot(snapshot.Data); err != nil {
			//		log.Panic(err)
			//	}
			//}
			continue
		}

		for _, data := range commit.data {
			var dataKv kv
			dec := gob.NewDecoder(bytes.NewBufferString(data))
			if err := dec.Decode(&dataKv); err != nil {
				log.Fatalf("raftexample: could not decode message (%v)", err)
			}
			//s.mu.Lock()
			s.data[dataKv.Key] = dataKv.Val
			//s.mu.Unlock()
		}
		close(commit.applyDoneC)
	}
	if err, ok := <-errorC; ok {
		log.Fatal(err)
	}
}

func (s *Kvstore) GetSnapshot() ([]byte, error) {
	//s.mu.RLock()
	//defer s.mu.RUnlock()
	return json.Marshal(s.data)
}
