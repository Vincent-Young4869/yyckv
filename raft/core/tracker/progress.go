package tracker

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

// BecomeReplicate transitions into StateReplicate, resetting Next to Match+1.
func (pr *Progress) BecomeReplicate() {
	pr.ResetState(StateReplicate)
	pr.Next = pr.Match + 1
}
