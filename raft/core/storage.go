package core

import (
	"errors"
	"sync"
	pb "yyckv/raft/raftpb"
)

// ErrCompacted is returned by Storage.Entries/Compact when a requested
// index is unavailable because it predates the last snapshot.
var ErrCompacted = errors.New("requested index is unavailable due to compaction")

// ErrSnapOutOfDate is returned by Storage.CreateSnapshot when a requested
// index is older than the existing snapshot.
var ErrSnapOutOfDate = errors.New("requested index is older than the existing snapshot")

// ErrUnavailable is returned by Storage interface when the requested log entries
// are unavailable.
var ErrUnavailable = errors.New("requested entry at index is unavailable")

// ErrSnapshotTemporarilyUnavailable is returned by the Storage interface when the required
// snapshot is temporarily unavailable.
var ErrSnapshotTemporarilyUnavailable = errors.New("snapshot is temporarily unavailable")

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

// NewMemoryStorage creates an empty MemoryStorage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		// When starting from scratch populate the list with a dummy entry at term zero.
		ents: make([]pb.Entry, 1),
	}
}

func (m *MemoryStorage) InitialState() (pb.HardState, pb.ConfState, error) {
	m.callStats.initialState++
	return m.hardState, m.snapshot.Metadata.ConfState, nil
}

func (m *MemoryStorage) Entries(lo, hi, maxSize uint64) ([]pb.Entry, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MemoryStorage) Term(i uint64) (uint64, error) {
	m.Lock()
	defer m.Unlock()
	m.callStats.term++
	offset := m.ents[0].Index
	if i < offset {
		return 0, ErrCompacted
	}
	if int(i-offset) >= len(m.ents) {
		return 0, ErrUnavailable
	}
	return m.ents[i-offset].Term, nil
}

func (m *MemoryStorage) LastIndex() (uint64, error) {
	m.Lock()
	defer m.Unlock()
	m.callStats.lastIndex++
	return m.lastIndex(), nil
}

func (m *MemoryStorage) lastIndex() uint64 {
	// The MemoryStorage can have a snapshot. The m.ents slice starts after the snapshot.
	// m.ents[0].Index = snapshot.Metadata.Index.
	return m.ents[0].Index + uint64(len(m.ents)) - 1
}

// FirstIndex implements the Storage interface.
func (m *MemoryStorage) FirstIndex() (uint64, error) {
	m.Lock()
	defer m.Unlock()
	m.callStats.firstIndex++
	return m.firstIndex(), nil
}

func (m *MemoryStorage) firstIndex() uint64 {
	return m.ents[0].Index + 1
}

// Snapshot implements the Storage interface.
func (m *MemoryStorage) Snapshot() (pb.Snapshot, error) {
	m.Lock()
	defer m.Unlock()
	m.callStats.snapshot++
	return m.snapshot, nil
}
