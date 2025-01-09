package core

import (
	"log"
	logger "yyckv/raft/log"
	pb "yyckv/raft/raftpb"
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

func (l *raftLog) maybeAppend(index, logTerm, committed uint64, ents ...pb.Entry) (lastnewi uint64, ok bool) {
	//if !l.matchTerm(index, logTerm) {
	//	return 0, false
	//}

	lastnewi = index + uint64(len(ents))
	//ci := l.findConflict(ents)
	//switch {
	//case ci == 0:
	//case ci <= l.committed:
	//	l.logger.Panicf("entry %d conflict with committed entry [committed(%d)]", ci, l.committed)
	//default:
	//	offset := index + 1
	//	if ci-offset > uint64(len(ents)) {
	//		l.logger.Panicf("index, %d, is out of range [%d]", ci-offset, len(ents))
	//	}
	//	l.append(ents[ci-offset:]...)
	//}
	//l.commitTo(min(committed, lastnewi))
	return lastnewi, true
}

func (l *raftLog) append(ents ...pb.Entry) uint64 {
	if len(ents) == 0 {
		return l.lastIndex()
	}
	//if after := ents[0].Index - 1; after < l.committed {
	//	l.logger.Panicf("after(%d) is out of range [committed(%d)]", after, l.committed)
	//}
	//l.unstable.truncateAndAppend(ents)
	return l.lastIndex()
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

func (l *raftLog) maybeCommit(maxIndex, term uint64) bool {
	// NB: term should never be 0 on a commit because the leader campaigns at
	// least at term 1. But if it is 0 for some reason, we don't want to consider
	// this a term match in case zeroTermOnOutOfBounds returns 0.
	//if maxIndex > l.committed && term != 0 && l.zeroTermOnOutOfBounds(l.term(maxIndex)) == term {
	//	l.commitTo(maxIndex)
	//	return true
	//}
	return false
}
