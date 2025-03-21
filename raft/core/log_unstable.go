package core

import (
	logger "yyckv/raft/log"
	pb "yyckv/raft/raftpb"
)

type unstable struct {
	// the incoming unstable snapshot, if any.
	snapshot *pb.Snapshot
	// all entries that have not yet been written to storage.
	entries []pb.Entry
	// entries[i] has raft log position i+offset.
	offset uint64

	// if true, snapshot is being written to storage.
	snapshotInProgress bool
	// entries[:offsetInProgress-offset] are being written to storage.
	// Like offset, offsetInProgress is exclusive, meaning that it
	// contains the index following the largest in-progress entry.
	// Invariant: offset <= offsetInProgress
	offsetInProgress uint64

	logger logger.Logger
}

// maybeFirstIndex returns the index of the first possible entry in entries
// if it has a snapshot.
func (u *unstable) maybeFirstIndex() (uint64, bool) {
	if u.snapshot != nil {
		return u.snapshot.Metadata.Index + 1, true
	}
	return 0, false
}

// maybeLastIndex returns the last index if it has at least one
// unstable entry or snapshot.
func (u *unstable) maybeLastIndex() (uint64, bool) {
	if l := len(u.entries); l != 0 {
		return u.offset + uint64(l) - 1, true
	}
	if u.snapshot != nil {
		return u.snapshot.Metadata.Index, true
	}
	return 0, false
}

// maybeTerm returns the term of the entry at index i, if there
// is any.
func (u *unstable) maybeTerm(i uint64) (uint64, bool) {
	if i < u.offset {
		if u.snapshot != nil && u.snapshot.Metadata.Index == i {
			return u.snapshot.Metadata.Term, true
		}
		return 0, false
	}

	last, ok := u.maybeLastIndex()
	if !ok {
		return 0, false
	}
	if i > last {
		return 0, false
	}

	return u.entries[i-u.offset].Term, true
}

func (u *unstable) truncateAndAppend(ents []pb.Entry) {
	fromIndex := ents[0].Index
	switch {
	case fromIndex == u.offset+uint64(len(u.entries)):
		// fromIndex is the next index in the u.entries, so append directly.
		u.entries = append(u.entries, ents...)
	case fromIndex <= u.offset:
		u.logger.Infof("replace the unstable entries from index %d", fromIndex)
		// The log is being truncated to before our current offset
		// portion, so set the offset and replace the entries.
		u.entries = ents
		u.offset = fromIndex
		u.offsetInProgress = u.offset
	default:
		// Truncate to fromIndex (exclusive), and append the new entries.
		u.logger.Infof("truncate the unstable entries before index %d", fromIndex)
		keep := u.slice(u.offset, fromIndex) // NB: appending to this slice is safe,
		u.entries = append(keep, ents...)    // and will reallocate/copy it
		// Only in-progress entries before fromIndex are still considered to be
		// in-progress.
		u.offsetInProgress = min(u.offsetInProgress, fromIndex)
	}
}

func (u *unstable) slice(lo uint64, hi uint64) []pb.Entry {
	u.mustCheckOutOfBounds(lo, hi)
	// NB: use the full slice expression to limit what the caller can do with the
	// returned slice. For example, an append will reallocate and copy this slice
	// instead of corrupting the neighbouring u.entries.
	return u.entries[lo-u.offset : hi-u.offset : hi-u.offset]
}

// u.offset <= lo <= hi <= u.offset+len(u.entries)
func (u *unstable) mustCheckOutOfBounds(lo, hi uint64) {
	if lo > hi {
		u.logger.Panicf("invalid unstable.slice %d > %d", lo, hi)
	}
	upper := u.offset + uint64(len(u.entries))
	if lo < u.offset || hi > upper {
		u.logger.Panicf("unstable.slice[%d,%d) out of bound [%d,%d]", lo, hi, u.offset, upper)
	}
}
