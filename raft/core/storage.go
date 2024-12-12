package core

import (
	"sync"
	pb "yyckv/raft/raftpb"
)

type Storage interface {
	// TODO(tbg): split this into two interfaces, LogStorage and StateStorage.

	InitialState() (pb.HardState, pb.ConfState, error)

	Entries(lo, hi, maxSize uint64) ([]pb.Entry, error)

	Term(i uint64) (uint64, error)

	LastIndex() (uint64, error)

	FirstIndex() (uint64, error)

	Snapshot() (pb.Snapshot, error)
}

type inMemStorageCallStats struct {
	initialState, firstIndex, lastIndex, entries, term, snapshot int
}

// MemoryStorage implements the Storage interface backed by an
// in-memory array.
type MemoryStorage struct {
	// Protects access to all fields. Most methods of MemoryStorage are
	// run on the raft goroutine, but Append() is run on an application
	// goroutine.
	sync.Mutex

	hardState pb.HardState
	snapshot  pb.Snapshot
	// ents[i] has raft log position i+snapshot.Metadata.Index
	ents []pb.Entry

	callStats inMemStorageCallStats
}

func (m *MemoryStorage) InitialState() (pb.HardState, pb.ConfState, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MemoryStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MemoryStorage) Term(i uint64) (uint64, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MemoryStorage) LastIndex() (uint64, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MemoryStorage) FirstIndex() (uint64, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MemoryStorage) Snapshot() (pb.Snapshot, error) {
	//TODO implement me
	panic("implement me")
}
