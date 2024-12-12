package core

import (
	"log"
	logger "yyckv/raft/log"
)

type raftLog struct {
	// storage contains all stable entries since the last snapshot.
	storage Storage

	// unstable contains all unstable entries and snapshot.
	// they will be saved into storage.
	unstable unstable

	// committed is the highest log position that is known to be in
	// stable storage on a quorum of nodes.
	committed uint64
	// applying is the highest log position that the application has
	// been instructed to apply to its state machine. Some of these
	// entries may be in the process of applying and have not yet
	// reached applied.
	// Use: The field is incremented when accepting a Ready struct.
	// Invariant: applied <= applying && applying <= committed
	applying uint64
	// applied is the highest log position that the application has
	// successfully applied to its state machine.
	// Use: The field is incremented when advancing after the committed
	// entries in a Ready struct have been applied (either synchronously
	// or asynchronously).
	// Invariant: applied <= committed
	applied uint64

	logger logger.Logger

	// maxApplyingEntsSize limits the outstanding byte size of the messages
	// returned from calls to nextCommittedEnts that have not been acknowledged
	// by a call to appliedTo.
	maxApplyingEntsSize entryEncodingSize
	// applyingEntsSize is the current outstanding byte size of the messages
	// returned from calls to nextCommittedEnts that have not been acknowledged
	// by a call to appliedTo.
	applyingEntsSize entryEncodingSize
	// applyingEntsPaused is true when entry application has been paused until
	// enough progress is acknowledged.
	applyingEntsPaused bool
}

func newLogWithSize(storage Storage, logger logger.Logger, maxApplyingEntsSize entryEncodingSize) *raftLog {
	if storage == nil {
		log.Panic("storage must not be nil")
	}
	log := &raftLog{
		storage:             storage,
		logger:              logger,
		maxApplyingEntsSize: maxApplyingEntsSize,
	}
	firstIndex, err := storage.FirstIndex()
	if err != nil {
		panic(err) // TODO(bdarnell)
	}
	lastIndex, err := storage.LastIndex()
	if err != nil {
		panic(err) // TODO(bdarnell)
	}
	log.unstable.offset = lastIndex + 1
	log.unstable.offsetInProgress = lastIndex + 1
	log.unstable.logger = logger
	// Initialize our committed and applied pointers to the time of the last compaction.
	log.committed = firstIndex - 1
	log.applying = firstIndex - 1
	log.applied = firstIndex - 1

	return log
}

func (l *raftLog) firstIndex() uint64 {
	// TODO
	return 0
}

func (l *raftLog) lastIndex() uint64 {
	// TODO
	return 0
}

func (l *raftLog) lastTerm() uint64 {
	t, err := l.term(l.lastIndex())
	if err != nil {
		l.logger.Panicf("unexpected error when getting the last term (%v)", err)
	}
	return t
}

func (l *raftLog) term(i uint64) (uint64, error) {
	// TODO
	return 0, nil
}
