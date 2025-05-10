package tracker

import "fmt"

type Progress struct {
	Match, Next uint64

	State StateType

	PendingSnapshot uint64

	RecentActive bool

	MsgAppFlowPaused bool

	//Inflights *Inflights

	IsLearner bool
}

// ResetState moves the Progress into the specified State, resetting MsgAppFlowPaused,
// PendingSnapshot, and Inflights.
func (pr *Progress) ResetState(state StateType) {
	pr.MsgAppFlowPaused = false
	pr.PendingSnapshot = 0
	pr.State = state
	//pr.Inflights.reset()
}

// ProgressMap is a map of *Progress.
type ProgressMap map[uint64]*Progress

func (pr *Progress) BecomeProbe() {
	// If the original state is StateSnapshot, progress knows that
	// the pending snapshot has been sent to this peer successfully, then
	// probes from pendingSnapshot + 1.
	if pr.State == StateSnapshot {
		pendingSnapshot := pr.PendingSnapshot
		pr.ResetState(StateProbe)
		pr.Next = max(pr.Match+1, pendingSnapshot+1)
	} else {
		pr.ResetState(StateProbe)
		pr.Next = pr.Match + 1
	}
}

// BecomeReplicate transitions into StateReplicate, resetting Next to Match+1.
func (pr *Progress) BecomeReplicate() {
	pr.ResetState(StateReplicate)
	pr.Next = pr.Match + 1
}

// UpdateOnEntriesSend updates the progress on the given number of consecutive
// entries being sent in a MsgApp, with the given total bytes size, appended at
// and after the given log index.
func (pr *Progress) UpdateOnEntriesSend(entries int, bytes, nextIndex uint64) error {
	switch pr.State {
	case StateReplicate:
		if entries > 0 {
			//last := nextIndex + uint64(entries) - 1
			//pr.OptimisticUpdate(last)
			//pr.Inflights.Add(last, bytes)
		}
		// If this message overflows the in-flights tracker, or it was already full,
		// consider this message being a probe, so that the flow is paused.
		//pr.MsgAppFlowPaused = pr.Inflights.Full()
	case StateProbe:
		// TODO: this condition captures the previous behaviour,
		// but we should set MsgAppFlowPaused unconditionally for simplicity, because any
		// MsgApp in StateProbe is a probe, not only non-empty ones.
		if entries > 0 {
			pr.MsgAppFlowPaused = true
		}
	default:
		return fmt.Errorf("sending append in unhandled state %s", pr.State)
	}
	return nil
}

func (pr *Progress) MaybeUpdate(n uint64) bool {
	var updated bool
	if pr.Match < n {
		pr.Match = n
		updated = true
		pr.MsgAppFlowPaused = false
	}
	pr.Next = max(pr.Next, n+1)
	return updated
}

func (pr *Progress) IsPaused() bool {
	switch pr.State {
	case StateProbe:
		return pr.MsgAppFlowPaused
	case StateReplicate:
		return pr.MsgAppFlowPaused
	case StateSnapshot:
		return true
	default:
		panic("unexpected state")
	}
}
