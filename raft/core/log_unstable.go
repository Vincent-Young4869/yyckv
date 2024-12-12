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
