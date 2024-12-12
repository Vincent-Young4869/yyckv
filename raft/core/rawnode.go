package core

import (
	"errors"
	pb "yyckv/raft/raftpb"
)

// ErrStepLocalMsg is returned when try to step a local raft message
var ErrStepLocalMsg = errors.New("raft: cannot step raft local message")

// ErrStepPeerNotFound is returned when try to step a response message
// but there is no peer found in raft.trk for that node.
var ErrStepPeerNotFound = errors.New("raft: cannot step as peer not found")

// RawNode is a thread-unsafe Node.
// The methods of this struct correspond to the methods of Node and are described
// more fully there.
type RawNode struct {
	raft               *raft
	asyncStorageWrites bool

	// Mutable fields.
	prevSoftSt     *SoftState
	prevHardSt     pb.HardState
	stepsOnAdvance []pb.Message
}

func NewRawNode(config *Config) (*RawNode, error) {
	r := newRaft(config)
	rn := &RawNode{
		raft: r,
	}
	rn.asyncStorageWrites = config.AsyncStorageWrites
	//ss := r.softState()
	//rn.prevSoftSt = &ss
	//rn.prevHardSt = r.hardState()
	return rn, nil
}

// Tick advances the internal logical clock by a single tick.
func (rn *RawNode) Tick() {
	rn.raft.tick()
}

// HasReady called when RawNode user need to check if any Ready pending.
func (rn *RawNode) HasReady() bool {
	// TODO(nvanbenschoten): order these cases in terms of cost and frequency.
	r := rn.raft
	//if softSt := r.softState(); !softSt.equal(rn.prevSoftSt) {
	//	return true
	//}
	//if hardSt := r.hardState(); !IsEmptyHardState(hardSt) && !isHardStateEqual(hardSt, rn.prevHardSt) {
	//	return true
	//}
	//if r.raftLog.hasNextUnstableSnapshot() {
	//	return true
	//}
	if len(r.msgs) > 0 || len(r.msgsAfterAppend) > 0 {
		return true
	}
	//if r.raftLog.hasNextUnstableEnts() || r.raftLog.hasNextCommittedEnts(rn.applyUnstableEntries()) {
	//	return true
	//}
	if len(r.readStates) != 0 {
		return true
	}
	return false
}

// Step advances the state machine using the given message.
func (rn *RawNode) Step(m pb.Message) error {
	// Ignore unexpected local messages receiving over network.
	if IsLocalMsg(m.Type) && !IsLocalMsgTarget(m.From) {
		return ErrStepLocalMsg
	}
	if IsResponseMsg(m.Type) && !IsLocalMsgTarget(m.From) && rn.raft.trk.Progress[m.From] == nil {
		return ErrStepPeerNotFound
	}
	return rn.raft.Step(m)
}

// Ready returns the outstanding work that the application needs to handle. This
// includes appending and applying entries or a snapshot, updating the HardState,
// and sending messages. The returned Ready() *must* be handled and subsequently
// passed back via Advance().
func (rn *RawNode) Ready() Ready {
	rd := rn.readyWithoutAccept()
	rn.acceptReady(rd)
	return rd
}

// readyWithoutAccept returns a Ready. This is a read-only operation, i.e. there
// is no obligation that the Ready must be handled.
func (rn *RawNode) readyWithoutAccept() Ready {
	r := rn.raft

	rd := Ready{
		//Entries:          r.raftLog.nextUnstableEnts(),
		//CommittedEntries: r.raftLog.nextCommittedEnts(rn.applyUnstableEntries()),
		Messages: r.msgs,
	}

	// TODO
	return rd
}

func (rn *RawNode) acceptReady(rd Ready) {
	// TODO
}

func (rn *RawNode) Advance(_ Ready) {
	// The actions performed by this function are encoded into stepsOnAdvance in
	// acceptReady. In earlier versions of this library, they were computed from
	// the provided Ready struct. Retain the unused parameter for compatibility.
	if rn.asyncStorageWrites {
		rn.raft.logger.Panicf("Advance must not be called when using AsyncStorageWrites")
	}
	for i, m := range rn.stepsOnAdvance {
		_ = rn.raft.Step(m)
		rn.stepsOnAdvance[i] = pb.Message{}
	}
	rn.stepsOnAdvance = rn.stepsOnAdvance[:0]
}
