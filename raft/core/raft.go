package core

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"sync"
	"yyckv/raft/core/confchange"
	"yyckv/raft/core/quorum"
	"yyckv/raft/core/tracker"
	logger "yyckv/raft/log"
	pb "yyckv/raft/raftpb"
)

const (
	// None is a placeholder node ID used when there is no leader.
	None uint64 = 0
	// LocalAppendThread is a reference to a local thread that saves unstable
	// log entries and snapshots to stable storage. The identifier is used as a
	// target for MsgStorageAppend messages when AsyncStorageWrites is enabled.
	LocalAppendThread uint64 = math.MaxUint64
	// LocalApplyThread is a reference to a local thread that applies committed
	// log entries to the local state machine. The identifier is used as a
	// target for MsgStorageApply messages when AsyncStorageWrites is enabled.
	LocalApplyThread uint64 = math.MaxUint64 - 1
)

// StateType represents the role of a node in a cluster.
type StateType uint64

// Possible values for StateType.
const (
	StateFollower StateType = iota
	StateCandidate
	StateLeader
	StatePreCandidate
	numStates
)

type CampaignType string

// Possible values for CampaignType
const (
	// campaignPreElection represents the first phase of a normal election when
	// Config.PreVote is true.
	campaignPreElection CampaignType = "CampaignPreElection"
	// campaignElection represents a normal (time-based) election (the second phase
	// of the election when Config.PreVote is true).
	campaignElection CampaignType = "CampaignElection"
	// campaignTransfer represents the type of leader transfer
	campaignTransfer CampaignType = "CampaignTransfer"
)

const noLimit = math.MaxUint64

// ErrProposalDropped is returned when the proposal is ignored by some cases,
// so that the proposer can be notified and fail fast.
var ErrProposalDropped = errors.New("raft proposal dropped")

// lockedRand is a small wrapper around rand.Rand to provide
// synchronization among multiple raft groups. Only the methods needed
// by the code are exposed (e.g. Intn).
type lockedRand struct {
	mu sync.Mutex
}

func (r *lockedRand) Intn(n int) int {
	r.mu.Lock()
	v, _ := rand.Int(rand.Reader, big.NewInt(int64(n)))
	r.mu.Unlock()
	return int(v.Int64())
}

var globalRand = &lockedRand{}

var stmap = [...]string{
	"StateFollower",
	"StateCandidate",
	"StateLeader",
	"StatePreCandidate",
}

func (st StateType) String() string {
	return stmap[st]
}

type Config struct {
	// ID is the identity of the local raft. ID cannot be 0.
	ID uint64

	ElectionTick int

	HeartbeatTick int

	// TODO
	Storage Storage

	// TODO
	//Applied uint64

	AsyncStorageWrites bool

	MaxSizePerMsg uint64

	MaxCommittedSizePerReady uint64

	MaxUncommittedEntriesSize uint64

	MaxInflightMsgs int

	MaxInflightBytes uint64

	CheckQuorum bool

	PreVote bool

	Logger logger.Logger
}

func (c *Config) validate() error {
	if c.ID == None {
		return errors.New("cannot use none as id")
	}
	if IsLocalMsgTarget(c.ID) {
		return errors.New("cannot use local target as id")
	}

	if c.HeartbeatTick <= 0 {
		return errors.New("heartbeat tick must be greater than 0")
	}

	if c.ElectionTick <= c.HeartbeatTick {
		return errors.New("election tick must be greater than heartbeat tick")
	}

	if c.Storage == nil {
		return errors.New("storage cannot be nil")
	}

	if c.MaxUncommittedEntriesSize == 0 {
		c.MaxUncommittedEntriesSize = noLimit
	}

	// default MaxCommittedSizePerReady to MaxSizePerMsg because they were
	// previously the same parameter.
	if c.MaxCommittedSizePerReady == 0 {
		c.MaxCommittedSizePerReady = c.MaxSizePerMsg
	}

	if c.MaxInflightMsgs <= 0 {
		return errors.New("max inflight messages must be greater than 0")
	}
	if c.MaxInflightBytes == 0 {
		c.MaxInflightBytes = noLimit
	} else if c.MaxInflightBytes < c.MaxSizePerMsg {
		return errors.New("max inflight bytes must be >= max message size")
	}

	if c.Logger == nil {
		c.Logger = logger.GetLogger()
	}

	//if c.ReadOnlyOption == ReadOnlyLeaseBased && !c.CheckQuorum {
	//	return errors.New("CheckQuorum must be enabled when ReadOnlyOption is ReadOnlyLeaseBased")
	//}
	return nil
}

type raft struct {
	id uint64

	Term uint64
	Vote uint64

	readStates []ReadState

	// the log
	raftLog *raftLog

	maxMsgSize         entryEncodingSize
	maxUncommittedSize entryPayloadSize

	trk tracker.ProgressTracker

	state StateType

	// isLearner is true if the local raft node is a learner.
	isLearner bool

	// msgs contains the list of messages that should be sent out immediately to
	// other nodes.
	//
	// Messages in this list must target other nodes.
	msgs []pb.Message
	// msgsAfterAppend contains the list of messages that should be sent after
	// the accumulated unstable state (e.g. term, vote, []entry, and snapshot)
	// has been persisted to durable storage. This includes waiting for any
	// unstable state that is already in the process of being persisted (i.e.
	// has already been handed out in a prior Ready struct) to complete.
	//
	// Messages in this list may target other nodes or may target this node.
	//
	// Messages in this list have the type MsgAppResp, MsgVoteResp, or
	// MsgPreVoteResp. See the comment in raft.send for details.
	msgsAfterAppend []pb.Message

	// the leader id
	lead uint64
	// leadTransferee is id of the leader transfer target when its value is not zero.
	// Follow the procedure defined in raft thesis 3.10.
	leadTransferee uint64
	// Only one conf change may be pending (in the log, but not yet
	// applied) at a time. This is enforced via pendingConfIndex, which
	// is set to a value >= the log index of the latest pending
	// configuration change (if any). Config changes are only allowed to
	// be proposed if the leader's applied index is greater than this
	// value.
	pendingConfIndex uint64
	// disableConfChangeValidation is Config.DisableConfChangeValidation,
	// see there for details.
	disableConfChangeValidation bool

	// number of ticks since it reached last electionTimeout when it is leader
	// or candidate.
	// number of ticks since it reached last electionTimeout or received a
	// valid message from current leader when it is a follower.
	electionElapsed int

	// number of ticks since it reached last heartbeatTimeout.
	// only leader keeps heartbeatElapsed.
	heartbeatElapsed int

	checkQuorum bool
	preVote     bool

	heartbeatTimeout int
	electionTimeout  int
	// randomizedElectionTimeout is a random number between
	// [electiontimeout, 2 * electiontimeout - 1]. It gets reset
	// when raft changes its state to follower or candidate.
	randomizedElectionTimeout int
	disableProposalForwarding bool
	stepDownOnRemoval         bool

	tick   func()
	step   stepFunc
	logger logger.Logger

	// pendingReadIndexMessages is used to store messages of type MsgReadIndex
	// that can't be answered as new leader didn't committed any log in
	// current term. Those will be handled as fast as first log is committed in
	// current term.
	pendingReadIndexMessages pb.Message
}

func newRaft(c *Config) *raft {
	if err := c.validate(); err != nil {
		panic(err.Error())
	}
	raftlog := newLogWithSize(c.Storage, c.Logger, entryEncodingSize(c.MaxCommittedSizePerReady))
	//hs, cs, err := c.Storage.InitialState()
	//if err != nil {
	//	panic(err) // TODO(bdarnell)
	//}

	r := &raft{
		id:        c.ID,
		lead:      None,
		isLearner: false,
		raftLog:   raftlog,
		//maxMsgSize:                  entryEncodingSize(c.MaxSizePerMsg),
		//maxUncommittedSize:          entryPayloadSize(c.MaxUncommittedEntriesSize),
		trk:              tracker.MakeProgressTracker(c.MaxInflightMsgs, c.MaxInflightBytes),
		electionTimeout:  c.ElectionTick,
		heartbeatTimeout: c.HeartbeatTick,
		logger:           c.Logger,
		checkQuorum:      c.CheckQuorum,
		preVote:          c.PreVote,
		//readOnly:                    newReadOnly(c.ReadOnlyOption),
		//disableProposalForwarding:   c.DisableProposalForwarding,
		//disableConfChangeValidation: c.DisableConfChangeValidation,
		//stepDownOnRemoval:           c.StepDownOnRemoval,
	}

	r.becomeFollower(r.Term, None)

	var nodesStrs []string
	for _, n := range r.trk.VoterNodes() {
		nodesStrs = append(nodesStrs, fmt.Sprintf("%x", n))
	}

	r.logger.Infof("newRaft %x [peers: [%s], term: %d, commit: %d, applied: %d, lastindex: %d, lastterm: %d]",
		r.id, strings.Join(nodesStrs, ","), r.Term, r.raftLog.committed, r.raftLog.applied, r.raftLog.lastIndex(), r.raftLog.lastTerm())
	return r
}

func (r *raft) Step(m pb.Message) error {
	// Handle the message term, which may result in our stepping down to a follower.
	switch {
	case m.Term == 0:
		// local message
	case m.Term > r.Term:
		if m.Type == pb.MsgVote || m.Type == pb.MsgPreVote {
			// TODO
		}
		switch {
		case m.Type == pb.MsgPreVote:
			// Never change our term in response to a PreVote
		case m.Type == pb.MsgPreVoteResp && !m.Reject:
			// We send pre-vote requests with a term in our future. If the
			// pre-vote is granted, we will increment our term when we get a
			// quorum. If it is not, the term comes from the node that
			// rejected our vote so we should become a follower at the new
			// term.
		default:
			r.logger.Infof("%x [term: %d] received a %s message with higher term from %x [term: %d]",
				r.id, r.Term, m.Type, m.From, m.Term)
			if m.Type == pb.MsgApp || m.Type == pb.MsgHeartbeat || m.Type == pb.MsgSnap {
				r.becomeFollower(m.Term, m.From)
			} else {
				r.becomeFollower(m.Term, None)
			}
		}

	case m.Term < r.Term:
		if (r.checkQuorum || r.preVote) && (m.Type == pb.MsgHeartbeat || m.Type == pb.MsgApp) {
			// We have received messages from a leader at a lower term. It is possible
			// that these messages were simply delayed in the network, but this could
			// also mean that this node has advanced its term number during a network
			// partition, and it is now unable to either win an election or to rejoin
			// the majority on the old term. If checkQuorum is false, this will be
			// handled by incrementing term numbers in response to MsgVote with a
			// higher term, but if checkQuorum is true we may not advance the term on
			// MsgVote and must generate other messages to advance the term. The net
			// result of these two features is to minimize the disruption caused by
			// nodes that have been removed from the cluster's configuration: a
			// removed node will send MsgVotes (or MsgPreVotes) which will be ignored,
			// but it will not receive MsgApp or MsgHeartbeat, so it will not create
			// disruptive term increases, by notifying leader of this node's activeness.
			// The above comments also true for Pre-Vote
			//
			// When follower gets isolated, it soon starts an election ending
			// up with a higher term than leader, although it won't receive enough
			// votes to win the election. When it regains connectivity, this response
			// with "pb.MsgAppResp" of higher term would force leader to step down.
			// However, this disruption is inevitable to free this stuck node with
			// fresh election. This can be prevented with Pre-Vote phase.
			r.send(pb.Message{To: m.From, Type: pb.MsgAppResp})
		} else if m.Type == pb.MsgPreVote {
			// Before Pre-Vote enable, there may have candidate with higher term,
			// but less log. After update to Pre-Vote, the cluster may deadlock if
			// we drop messages with a lower term.
			r.logger.Infof("%x [logterm: %d, index: %d, vote: %x] rejected %s from %x [logterm: %d, index: %d] at term %d",
				r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), r.Vote, m.Type, m.From, m.LogTerm, m.Index, r.Term)
			r.send(pb.Message{To: m.From, Term: r.Term, Type: pb.MsgPreVoteResp, Reject: true})
		} else if m.Type == pb.MsgStorageAppendResp {
			if m.Index != 0 {
				// Don't consider the appended log entries to be stable because
				// they may have been overwritten in the unstable log during a
				// later term. See the comment in newStorageAppendResp for more
				// about this race.
				r.logger.Infof("%x [term: %d] ignored entry appends from a %s message with lower term [term: %d]",
					r.id, r.Term, m.Type, m.Term)
			}
			if m.Snapshot != nil {
				// TODO
			}
		} else {
			// ignore other cases
			r.logger.Infof("%x [term: %d] ignored a %s message with lower term from %x [term: %d]",
				r.id, r.Term, m.Type, m.From, m.Term)
		}
		return nil
	}

	switch m.Type {
	case pb.MsgHup:
		if r.preVote {
			r.hup(campaignPreElection)
		} else {
			r.hup(campaignElection)
		}
	case pb.MsgStorageAppendResp:

	case pb.MsgStorageApplyResp:

	case pb.MsgVote, pb.MsgPreVote:
		// We can vote if this is a repeat of a vote we've already cast...
		canVote := r.Vote == m.From ||
			// ...we haven't voted and we don't think there's a leader yet in this term...
			(r.Vote == None && r.lead == None) ||
			// ...or this is a PreVote for a future term...
			(m.Type == pb.MsgPreVote && m.Term > r.Term)
		// ...and we believe the candidate is up to date.
		if canVote && r.raftLog.isUpToDate(m.Index, m.LogTerm) {
			// Note: it turns out that that learners must be allowed to cast votes.
			// This seems counter- intuitive but is necessary in the situation in which
			// a learner has been promoted (i.e. is now a voter) but has not learned
			// about this yet.
			// For example, consider a group in which id=1 is a learner and id=2 and
			// id=3 are voters. A configuration change promoting 1 can be committed on
			// the quorum `{2,3}` without the config change being appended to the
			// learner's log. If the leader (say 2) fails, there are de facto two
			// voters remaining. Only 3 can win an election (due to its log containing
			// all committed entries), but to do so it will need 1 to vote. But 1
			// considers itself a learner and will continue to do so until 3 has
			// stepped up as leader, replicates the conf change to 1, and 1 applies it.
			// Ultimately, by receiving a request to vote, the learner realizes that
			// the candidate believes it to be a voter, and that it should act
			// accordingly. The candidate's config may be stale, too; but in that case
			// it won't win the election, at least in the absence of the bug discussed
			// in:
			// https://github.com/etcd-io/etcd/issues/7625#issuecomment-488798263.
			r.logger.Infof("%x [logterm: %d, index: %d, vote: %x] cast %s for %x [logterm: %d, index: %d] at term %d",
				r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), r.Vote, m.Type, m.From, m.LogTerm, m.Index, r.Term)
			// When responding to Msg{Pre,}Vote messages we include the term
			// from the message, not the local term. To see why, consider the
			// case where a single node was previously partitioned away and
			// it's local term is now out of date. If we include the local term
			// (recall that for pre-votes we don't update the local term), the
			// (pre-)campaigning node on the other end will proceed to ignore
			// the message (it ignores all out of date messages).
			// The term in the original message and current local term are the
			// same in the case of regular votes, but different for pre-votes.
			r.send(pb.Message{To: m.From, Term: m.Term, Type: voteRespMsgType(m.Type)})
			if m.Type == pb.MsgVote {
				// Only record real votes.
				r.electionElapsed = 0
				r.Vote = m.From
			}
		} else {
			r.logger.Infof("%x [logterm: %d, index: %d, vote: %x] rejected %s from %x [logterm: %d, index: %d] at term %d",
				r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), r.Vote, m.Type, m.From, m.LogTerm, m.Index, r.Term)
			r.send(pb.Message{To: m.From, Term: r.Term, Type: voteRespMsgType(m.Type), Reject: true})
		}
	default:
		err := r.step(r, m)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *raft) hup(t CampaignType) {
	if r.state == StateLeader {
		r.logger.Debugf("%x ignoring MsgHup because already leader", r.id)
		return
	}

	if !r.promotable() {
		r.logger.Warningf("%x is unpromotable and can not campaign", r.id)
		return
	}
	//if r.hasUnappliedConfChanges() {
	//	r.logger.Warningf("%x cannot campaign at term %d since there are still pending configuration changes to apply", r.id, r.Term)
	//	return
	//}

	r.logger.Infof("%x is starting a new election at term %d", r.id, r.Term)
	r.campaign(t)
}

func (r *raft) campaign(t CampaignType) {
	if !r.promotable() {
		// This path should not be hit (callers are supposed to check), but
		// better safe than sorry.
		r.logger.Warningf("%x is unpromotable; campaign() should have been called", r.id)
	}
	var term uint64
	var voteMsg pb.MessageType
	if t == campaignPreElection {
		r.becomePreCandidate()
		voteMsg = pb.MsgPreVote
		// PreVote RPCs are sent for the next term before we've incremented r.Term.
		term = r.Term + 1
	} else {
		r.becomeCandidate()
		voteMsg = pb.MsgVote
		term = r.Term
	}
	var ids []uint64
	{
		idMap := r.trk.Voters.IDs()
		ids = make([]uint64, 0, len(idMap))
		for id := range idMap {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	for _, id := range ids {
		if id == r.id {
			// The candidate votes for itself and should account for this self
			// vote once the vote has been durably persisted (since it doesn't
			// send a MsgVote to itself). This response message will be added to
			// msgsAfterAppend and delivered back to this node after the vote
			// has been written to stable storage.
			r.send(pb.Message{To: id, Term: term, Type: voteRespMsgType(voteMsg)})
			continue
		}
		r.logger.Infof("%x [logterm: %d, index: %d] sent %s request to %x at term %d",
			r.id, r.raftLog.lastTerm(), r.raftLog.lastIndex(), voteMsg, id, r.Term)

		var ctx []byte
		if t == campaignTransfer {
			ctx = []byte(t)
		}
		r.send(pb.Message{To: id, Term: term, Type: voteMsg, Index: r.raftLog.lastIndex(), LogTerm: r.raftLog.lastTerm(), Context: ctx})
	}
}

func (r *raft) poll(id uint64, t pb.MessageType, v bool) (granted int, rejected int, result quorum.VoteResult) {
	if v {
		r.logger.Infof("%x received %s from %x at term %d", r.id, t, id, r.Term)
	} else {
		r.logger.Infof("%x received %s rejection from %x at term %d", r.id, t, id, r.Term)
	}
	r.trk.RecordVote(id, v)
	return r.trk.TallyVotes()
}

type stepFunc func(r *raft, m pb.Message) error

func (r *raft) hasLeader() bool { return r.lead != None }

func (r *raft) becomeFollower(term uint64, lead uint64) {
	r.step = stepFollower
	r.reset(term)
	r.tick = r.tickElection
	r.lead = lead
	r.state = StateFollower
	r.logger.Infof("%x became follower at term %d", r.id, r.Term)
}

func (r *raft) becomeCandidate() {
	// TODO(xiangli) remove the panic when the raft implementation is stable
	if r.state == StateLeader {
		panic("invalid transition [leader -> candidate]")
	}
	r.step = stepCandidate
	r.reset(r.Term + 1)
	r.tick = r.tickElection
	r.Vote = r.id
	r.state = StateCandidate
	r.logger.Infof("%x became candidate at term %d", r.id, r.Term)
}

func stepCandidate(r *raft, m pb.Message) error {
	// Only handle vote responses corresponding to our candidacy (while in
	// StateCandidate, we may get stale MsgPreVoteResp messages in this term from
	// our pre-candidate state).
	var myVoteRespType pb.MessageType
	if r.state == StatePreCandidate {
		myVoteRespType = pb.MsgPreVoteResp
	} else {
		myVoteRespType = pb.MsgVoteResp
	}
	switch m.Type {
	case pb.MsgProp:
		r.logger.Infof("%x no leader at term %d; dropping proposal", r.id, r.Term)
		return ErrProposalDropped
	case pb.MsgApp:
		r.becomeFollower(m.Term, m.From) // always m.Term == r.Term
		//r.handleAppendEntries(m)
	case pb.MsgHeartbeat:
		r.becomeFollower(m.Term, m.From) // always m.Term == r.Term
		r.handleHeartbeat(m)
	case pb.MsgSnap:
		r.becomeFollower(m.Term, m.From) // always m.Term == r.Term
		//r.handleSnapshot(m)
	case myVoteRespType:
		gr, rj, res := r.poll(m.From, m.Type, !m.Reject)
		r.logger.Infof("%x has received %d %s votes and %d vote rejections", r.id, gr, m.Type, rj)
		switch res {
		case quorum.VoteWon:
			if r.state == StatePreCandidate {
				r.campaign(campaignElection)
			} else {
				r.becomeLeader()
				r.bcastAppend()
			}
		case quorum.VoteLost:
			// pb.MsgPreVoteResp contains future term of pre-candidate
			// m.Term > r.Term; reuse r.Term
			r.becomeFollower(r.Term, None)
		}
	case pb.MsgTimeoutNow:
		r.logger.Debugf("%x [term %d state %v] ignored MsgTimeoutNow from %x", r.id, r.Term, r.state, m.From)
	}
	return nil
}

func (r *raft) becomePreCandidate() {
	// TODO(xiangli) remove the panic when the raft implementation is stable
	if r.state == StateLeader {
		panic("invalid transition [leader -> pre-candidate]")
	}
	// Becoming a pre-candidate changes our step functions and state,
	// but doesn't change anything else. In particular it does not increase
	// r.Term or change r.Vote.
	r.step = stepCandidate
	r.trk.ResetVotes()
	r.tick = r.tickElection
	r.lead = None
	r.state = StatePreCandidate
	r.logger.Infof("%x became pre-candidate at term %d", r.id, r.Term)
}

func stepFollower(r *raft, m pb.Message) error {
	switch m.Type {
	case pb.MsgProp:
		if r.lead == None {
			r.logger.Infof("%x no leader at term %d; dropping proposal", r.id, r.Term)
			return ErrProposalDropped
		} else if r.disableProposalForwarding {
			r.logger.Infof("%x not forwarding to leader %x at term %d; dropping proposal", r.id, r.lead, r.Term)
			return ErrProposalDropped
		}
		m.To = r.lead
		r.send(m)
	case pb.MsgApp:
		r.electionElapsed = 0
		r.lead = m.From
		r.handleAppendEntries(m)
	case pb.MsgHeartbeat:
		r.electionElapsed = 0
		r.lead = m.From
		r.handleHeartbeat(m)
	case pb.MsgSnap:
		//r.electionElapsed = 0
		//r.lead = m.From
		//r.handleSnapshot(m)
	case pb.MsgTransferLeader:
		if r.lead == None {
			r.logger.Infof("%x no leader at term %d; dropping leader transfer msg", r.id, r.Term)
			return nil
		}
		m.To = r.lead
		r.send(m)
	case pb.MsgForgetLeader:
		//if r.readOnly.option == ReadOnlyLeaseBased {
		//	r.logger.Error("ignoring MsgForgetLeader due to ReadOnlyLeaseBased")
		//	return nil
		//}
		//if r.lead != None {
		//	r.logger.Infof("%x forgetting leader %x at term %d", r.id, r.lead, r.Term)
		//	r.lead = None
		//}
	case pb.MsgTimeoutNow:
		r.logger.Infof("%x [term %d] received MsgTimeoutNow from %x and starts an election to get leadership.", r.id, r.Term, m.From)
		// Leadership transfers never use pre-vote even if r.preVote is true; we
		// know we are not recovering from a partition so there is no need for the
		// extra round trip.
		//r.hup(campaignTransfer)
	case pb.MsgReadIndex:
		if r.lead == None {
			r.logger.Infof("%x no leader at term %d; dropping index reading msg", r.id, r.Term)
			return nil
		}
		m.To = r.lead
		r.send(m)
	case pb.MsgReadIndexResp:
		if len(m.Entries) != 1 {
			r.logger.Errorf("%x invalid format of MsgReadIndexResp from %x, entries count: %d", r.id, m.From, len(m.Entries))
			return nil
		}
		r.readStates = append(r.readStates, ReadState{Index: m.Index, RequestCtx: m.Entries[0].Data})
	}
	return nil
}

func (r *raft) handleAppendEntries(m pb.Message) {
	if m.Index < r.raftLog.committed {
		r.send(pb.Message{To: m.From, Type: pb.MsgAppResp, Index: r.raftLog.committed})
		return
	}
	if mlastIndex, ok := r.raftLog.maybeAppend(m.Index, m.LogTerm, m.Commit, m.Entries...); ok {
		r.send(pb.Message{To: m.From, Type: pb.MsgAppResp, Index: mlastIndex})
		return
	}
	r.logger.Debugf("%x [logterm: %d, index: %d] rejected MsgApp [logterm: %d, index: %d] from %x",
		r.id, r.raftLog.zeroTermOnOutOfBounds(r.raftLog.term(m.Index)), m.Index, m.LogTerm, m.Index, m.From)

	// Our log does not match the leader's at index m.Index. Return a hint to the
	// leader - a guess on the maximal (index, term) at which the logs match. Do
	// this by searching through the follower's log for the maximum (index, term)
	// pair with a term <= the MsgApp's LogTerm and an index <= the MsgApp's
	// Index. This can help skip all indexes in the follower's uncommitted tail
	// with terms greater than the MsgApp's LogTerm.
	//
	// See the other caller for findConflictByTerm (in stepLeader) for a much more
	// detailed explanation of this mechanism.

	// NB: m.Index >= raftLog.committed by now (see the early return above), and
	// raftLog.lastIndex() >= raftLog.committed by invariant, so min of the two is
	// also >= raftLog.committed. Hence, the findConflictByTerm argument is within
	// the valid interval, which then will return a valid (index, term) pair with
	// a non-zero term (unless the log is empty). However, it is safe to send a zero
	// LogTerm in this response in any case, so we don't verify it here.
	hintIndex := min(m.Index, r.raftLog.lastIndex())
	hintIndex, hintTerm := r.raftLog.findConflictByTerm(hintIndex, m.LogTerm)
	r.send(pb.Message{
		To:         m.From,
		Type:       pb.MsgAppResp,
		Index:      m.Index,
		Reject:     true,
		RejectHint: hintIndex,
		LogTerm:    hintTerm,
	})
}

func (r *raft) handleHeartbeat(m pb.Message) {
	//r.raftLog.commitTo(m.Commit)
	r.send(pb.Message{To: m.From, Type: pb.MsgHeartbeatResp, Context: m.Context})
}

func (r *raft) tickElection() {
	r.electionElapsed++

	if r.promotable() && r.pastElectionTimeout() {
		r.electionElapsed = 0
		if err := r.Step(pb.Message{From: r.id, Type: pb.MsgHup}); err != nil {
			r.logger.Debugf("error occurred during election: %v", err)
		}
	}
}

func (r *raft) promotable() bool {
	pr := r.trk.Progress[r.id]
	return pr != nil && !pr.IsLearner
	//return pr != nil && !pr.IsLearner && !r.raftLog.hasNextOrInProgressSnapshot()
}

func (r *raft) pastElectionTimeout() bool {
	return r.electionElapsed >= r.randomizedElectionTimeout
}

func (r *raft) becomeLeader() {
	if r.state == StateFollower {
		panic("invalid transition [follower -> leader]")
	}
	r.step = stepLeader
	r.reset(r.Term)
	r.tick = r.tickHeartbeat
	r.lead = r.id
	r.state = StateLeader
	// Followers enter replicate mode when they've been successfully probed
	// (perhaps after having received a snapshot as a result). The leader is
	// trivially in this state. Note that r.reset() has initialized this
	// progress with the last index already.
	pr := r.trk.Progress[r.id]
	pr.BecomeReplicate()
	// The leader always has RecentActive == true; MsgCheckQuorum makes sure to
	// preserve this.
	pr.RecentActive = true

	// Conservatively set the pendingConfIndex to the last index in the
	// log. There may or may not be a pending config change, but it's
	// safe to delay any future proposals until we commit all our
	// pending log entries, and scanning the entire tail of the log
	// could be expensive.
	r.pendingConfIndex = r.raftLog.lastIndex()

	emptyEnt := pb.Entry{Data: nil}
	if !r.appendEntry(emptyEnt) {
		// This won't happen because we just called reset() above.
		r.logger.Panic("empty entry was dropped")
	}
	// The payloadSize of an empty entry is 0 (see TestPayloadSizeOfEmptyEntry),
	// so the preceding log append does not count against the uncommitted log
	// quota of the new leader. In other words, after the call to appendEntry,
	// r.uncommittedSize is still 0.
	r.logger.Infof("%x became leader at term %d", r.id, r.Term)
}

// tickHeartbeat is run by leaders to send a MsgBeat after r.heartbeatTimeout.
func (r *raft) tickHeartbeat() {
	r.heartbeatElapsed++
	r.electionElapsed++

	if r.electionElapsed >= r.electionTimeout {
		r.electionElapsed = 0
		if r.checkQuorum {
			if err := r.Step(pb.Message{From: r.id, Type: pb.MsgCheckQuorum}); err != nil {
				r.logger.Debugf("error occurred during checking sending heartbeat: %v", err)
			}
		}
		// If current leader cannot transfer leadership in electionTimeout, it becomes leader again.
		if r.state == StateLeader && r.leadTransferee != None {
			//r.abortLeaderTransfer()
		}
	}

	if r.state != StateLeader {
		return
	}

	if r.heartbeatElapsed >= r.heartbeatTimeout {
		r.heartbeatElapsed = 0
		if err := r.Step(pb.Message{From: r.id, Type: pb.MsgBeat}); err != nil {
			r.logger.Debugf("error occurred during checking sending heartbeat: %v", err)
		}
	}
}

func stepLeader(r *raft, m pb.Message) error {
	// These message types do not require any progress for m.From.
	switch m.Type {
	case pb.MsgBeat:
		r.bcastHeartbeat()
		return nil
	case pb.MsgCheckQuorum:
		return nil
	case pb.MsgProp:
		if len(m.Entries) == 0 {
			r.logger.Panicf("%x stepped empty MsgProp", r.id)
		}
		if r.trk.Progress[r.id] == nil {
			// If we are not currently a member of the range (i.e. this node
			// was removed from the configuration while serving as leader),
			// drop any new proposals.
			return ErrProposalDropped
		}
		if r.leadTransferee != None {
			r.logger.Debugf("%x [term %d] transfer leadership to %x is in progress; dropping proposal", r.id, r.Term, r.leadTransferee)
			return ErrProposalDropped
		}

		for i := range m.Entries {
			e := &m.Entries[i]
			var cc pb.ConfChangeI
			if e.Type == pb.EntryConfChange {
				var ccc pb.ConfChange
				if err := ccc.Unmarshal(e.Data); err != nil {
					panic(err)
				}
				cc = ccc
			} else if e.Type == pb.EntryConfChangeV2 {
				var ccc pb.ConfChangeV2
				if err := ccc.Unmarshal(e.Data); err != nil {
					panic(err)
				}
				cc = ccc
			}
			if cc != nil {
				alreadyPending := r.pendingConfIndex > r.raftLog.applied
				alreadyJoint := len(r.trk.Config.Voters[1]) > 0
				wantsLeaveJoint := len(cc.AsV2().Changes) == 0

				var failedCheck string
				if alreadyPending {
					failedCheck = fmt.Sprintf("possible unapplied conf change at index %d (applied to %d)", r.pendingConfIndex, r.raftLog.applied)
				} else if alreadyJoint && !wantsLeaveJoint {
					failedCheck = "must transition out of joint config first"
				} else if !alreadyJoint && wantsLeaveJoint {
					failedCheck = "not in joint state; refusing empty conf change"
				}

				if failedCheck != "" && !r.disableConfChangeValidation {
					r.logger.Infof("%x ignoring conf change %v at config %s: %s", r.id, cc, r.trk.Config, failedCheck)
					m.Entries[i] = pb.Entry{Type: pb.EntryNormal}
				} else {
					r.pendingConfIndex = r.raftLog.lastIndex() + uint64(i) + 1
				}
			}
		}

		if !r.appendEntry(m.Entries...) {
			return ErrProposalDropped
		}
		r.bcastAppend()
		return nil
	case pb.MsgReadIndex:
	case pb.MsgForgetLeader:
		return nil // noop on leader
	}

	// All other message types require a progress for m.From (pr).
	pr := r.trk.Progress[m.From]
	if pr == nil {
		r.logger.Debugf("%x no progress available for %x", r.id, m.From)
		return nil
	}
	switch m.Type {
	case pb.MsgAppResp:
		// NB: this code path is also hit from (*raft).advance, where the leader steps
		// an MsgAppResp to acknowledge the appended entries in the last Ready.

		pr.RecentActive = true

		if m.Reject {
			// TODO:
		} else {
			oldPaused := pr.IsPaused()
			// We want to update our tracking if the response updates our
			// matched index or if the response can move a probing peer back
			// into StateReplicate (see heartbeat_rep_recovers_from_probing.txt
			// for an example of the latter case).
			// NB: the same does not make sense for StateSnapshot - if `m.Index`
			// equals pr.Match we know we don't m.Index+1 in our log, so moving
			// back to replicating state is not useful; besides pr.PendingSnapshot
			// would prevent it.
			if pr.MaybeUpdate(m.Index) || (pr.Match == m.Index && pr.State == tracker.StateProbe) {
				switch {
				case pr.State == tracker.StateProbe:
					pr.BecomeReplicate()
				case pr.State == tracker.StateSnapshot && pr.Match+1 >= r.raftLog.firstIndex():
					// Note that we don't take into account PendingSnapshot to
					// enter this branch. No matter at which index a snapshot
					// was actually applied, as long as this allows catching up
					// the follower from the log, we will accept it. This gives
					// systems more flexibility in how they implement snapshots;
					// see the comments on PendingSnapshot.
					r.logger.Debugf("%x recovered from needing snapshot, resumed sending replication messages to %x [%s]", r.id, m.From, pr)
					// Transition back to replicating state via probing state
					// (which takes the snapshot into account). If we didn't
					// move to replicating state, that would only happen with
					// the next round of appends (but there may not be a next
					// round for a while, exposing an inconsistent RaftStatus).
					pr.BecomeProbe()
					pr.BecomeReplicate()
				case pr.State == tracker.StateReplicate:
					//pr.Inflights.FreeLE(m.Index)
				}

				if r.maybeCommit() {
					// committed index has progressed for the term, so it is safe
					// to respond to pending read index requests
					//releasePendingReadIndexMessages(r)
					r.bcastAppend()
				} else if oldPaused {
					// If we were paused before, this node may be missing the
					// latest commit index, so send it.
					r.sendAppend(m.From)
				}
				// We've updated flow control information above, which may
				// allow us to send multiple (size-limited) in-flight messages
				// at once (such as when transitioning from probe to
				// replicate, or when freeTo() covers multiple messages). If
				// we have more entries to send, send as many messages as we
				// can (without sending empty messages for the commit index)
				if r.id != m.From {
					for r.maybeSendAppend(m.From, false /* sendIfEmpty */) {
					}
				}
				// Transfer leadership is in progress.
				//if m.From == r.leadTransferee && pr.Match == r.raftLog.lastIndex() {
				//	r.logger.Infof("%x sent MsgTimeoutNow to %x after received MsgAppResp", r.id, m.From)
				//	r.sendTimeoutNow(m.From)
				//}
			}
		}
	case pb.MsgHeartbeatResp:
	case pb.MsgSnapStatus:
	case pb.MsgUnreachable:
	case pb.MsgTransferLeader:
	}
	return nil
}

func (r *raft) bcastAppend() {
	r.trk.Visit(func(id uint64, _ *tracker.Progress) {
		if id == r.id {
			return
		}
		r.sendAppend(id)
	})
}

// bcastHeartbeat sends RPC, without entries to all the peers.
func (r *raft) bcastHeartbeat() {
	lastCtx := ""
	//lastCtx := r.readOnly.lastPendingRequestCtx()
	if len(lastCtx) == 0 {
		r.bcastHeartbeatWithCtx(nil)
	} else {
		r.bcastHeartbeatWithCtx([]byte(lastCtx))
	}
}

func (r *raft) bcastHeartbeatWithCtx(ctx []byte) {
	r.trk.Visit(func(id uint64, _ *tracker.Progress) {
		if id == r.id {
			return
		}
		r.sendHeartbeat(id, ctx)
	})
}

// sendHeartbeat sends a heartbeat RPC to the given peer.
func (r *raft) sendHeartbeat(to uint64, ctx []byte) {
	// Attach the commit as min(to.matched, r.committed).
	// When the leader sends out heartbeat message,
	// the receiver(follower) might not be matched with the leader
	// or it might not have all the committed entries.
	// The leader MUST NOT forward the follower's commit to
	// an unmatched index.
	commit := min(r.trk.Progress[to].Match, r.raftLog.committed)
	m := pb.Message{
		To:      to,
		Type:    pb.MsgHeartbeat,
		Commit:  commit,
		Context: ctx,
	}

	r.send(m)
}

// send schedules persisting state to a stable storage and AFTER that
// sending the message (as part of next Ready message processing).
func (r *raft) send(m pb.Message) {
	if m.From == None {
		m.From = r.id
	}
	if m.Type == pb.MsgVote || m.Type == pb.MsgVoteResp || m.Type == pb.MsgPreVote || m.Type == pb.MsgPreVoteResp {
		if m.Term == 0 {
			// All {pre-,}campaign messages need to have the term set when
			// sending.
			// - MsgVote: m.Term is the term the node is campaigning for,
			//   non-zero as we increment the term when campaigning.
			// - MsgVoteResp: m.Term is the new r.Term if the MsgVote was
			//   granted, non-zero for the same reason MsgVote is
			// - MsgPreVote: m.Term is the term the node will campaign,
			//   non-zero as we use m.Term to indicate the next term we'll be
			//   campaigning for
			// - MsgPreVoteResp: m.Term is the term received in the original
			//   MsgPreVote if the pre-vote was granted, non-zero for the
			//   same reasons MsgPreVote is
			r.logger.Panicf("term should be set when sending %s", m.Type)
		}
	} else {
		if m.Term != 0 {
			r.logger.Panicf("term should not be set when sending %s (was %d)", m.Type, m.Term)
		}
		// do not attach term to MsgProp, MsgReadIndex
		// proposals are a way to forward to the leader and
		// should be treated as local message.
		// MsgReadIndex is also forwarded to leader.
		if m.Type != pb.MsgProp && m.Type != pb.MsgReadIndex {
			m.Term = r.Term
		}
	}
	if m.Type == pb.MsgAppResp || m.Type == pb.MsgVoteResp || m.Type == pb.MsgPreVoteResp {
		// If async storage writes are enabled, messages added to the msgs slice
		// are allowed to be sent out before unstable state (e.g. log entry
		// writes and election votes) have been durably synced to the local
		// disk.
		//
		// For most message types, this is not an issue. However, response
		// messages that relate to "voting" on either leader election or log
		// appends require durability before they can be sent. It would be
		// incorrect to publish a vote in an election before that vote has been
		// synced to stable storage locally. Similarly, it would be incorrect to
		// acknowledge a log append to the leader before that entry has been
		// synced to stable storage locally.
		//
		// Per the Raft thesis, section 3.8 Persisted state and server restarts:
		//
		// > Raft servers must persist enough information to stable storage to
		// > survive server restarts safely. In particular, each server persists
		// > its current term and vote; this is necessary to prevent the server
		// > from voting twice in the same term or replacing log entries from a
		// > newer leader with those from a deposed leader. Each server also
		// > persists new log entries before they are counted towards the entries’
		// > commitment; this prevents committed entries from being lost or
		// > “uncommitted” when servers restart
		//
		// To enforce this durability requirement, these response messages are
		// queued to be sent out as soon as the current collection of unstable
		// state (the state that the response message was predicated upon) has
		// been durably persisted. This unstable state may have already been
		// passed to a Ready struct whose persistence is in progress or may be
		// waiting for the next Ready struct to begin being written to Storage.
		// These messages must wait for all of this state to be durable before
		// being published.
		//
		// Rejected responses (m.Reject == true) present an interesting case
		// where the durability requirement is less unambiguous. A rejection may
		// be predicated upon unstable state. For instance, a node may reject a
		// vote for one peer because it has already begun syncing its vote for
		// another peer. Or it may reject a vote from one peer because it has
		// unstable log entries that indicate that the peer is behind on its
		// log. In these cases, it is likely safe to send out the rejection
		// response immediately without compromising safety in the presence of a
		// server restart. However, because these rejections are rare and
		// because the safety of such behavior has not been formally verified,
		// we err on the side of safety and omit a `&& !m.Reject` condition
		// above.
		r.msgsAfterAppend = append(r.msgsAfterAppend, m)
	} else {
		if m.To == r.id {
			r.logger.Panicf("message should not be self-addressed when sending %s", m.Type)
		}
		r.msgs = append(r.msgs, m)
	}
}

func (r *raft) sendAppend(to uint64) {
	r.maybeSendAppend(to, true)
}

// maybeSendAppend sends an append RPC with new entries to the given peer,
// if necessary. Returns true if a message was sent. The sendIfEmpty
// argument controls whether messages with no entries will be sent
// ("empty" messages are useful to convey updated Commit indexes, but
// are undesirable when we're sending multiple messages in a batch).
func (r *raft) maybeSendAppend(to uint64, sendIfEmpty bool) bool {
	pr := r.trk.Progress[to]
	//if pr.IsPaused() {
	//	return false
	//}

	lastIndex, nextIndex := pr.Next-1, pr.Next
	lastTerm, errt := r.raftLog.term(lastIndex)

	var ents []pb.Entry
	var erre error
	// In a throttled StateReplicate only send empty MsgApp, to ensure progress.
	// Otherwise, if we had a full Inflights and all inflight messages were in
	// fact dropped, replication to that follower would stall. Instead, an empty
	// MsgApp will eventually reach the follower (heartbeats responses prompt the
	// leader to send an append), allowing it to be acked or rejected, both of
	// which will clear out Inflights.
	//if pr.State != tracker.StateReplicate || !pr.Inflights.Full() {
	if pr.State != tracker.StateReplicate {
		ents, erre = r.raftLog.entries(nextIndex, r.maxMsgSize)
	}

	if len(ents) == 0 && !sendIfEmpty {
		return false
	}

	if errt != nil || erre != nil { // send snapshot if we failed to get term or entries
		if !pr.RecentActive {
			r.logger.Debugf("ignore sending snapshot to %x since it is not recently active", to)
			return false
		}

		//snapshot, err := r.raftLog.snapshot()
		//if err != nil {
		//	if err == ErrSnapshotTemporarilyUnavailable {
		//		r.logger.Debugf("%x failed to send snapshot to %x because snapshot is temporarily unavailable", r.id, to)
		//		return false
		//	}
		//	panic(err) // TODO(bdarnell)
		//}
		//if IsEmptySnap(snapshot) {
		//	panic("need non-empty snapshot")
		//}
		//sindex, sterm := snapshot.Metadata.Index, snapshot.Metadata.Term
		//r.logger.Debugf("%x [firstindex: %d, commit: %d] sent snapshot[index: %d, term: %d] to %x [%s]",
		//	r.id, r.raftLog.firstIndex(), r.raftLog.committed, sindex, sterm, to, pr)
		//pr.BecomeSnapshot(sindex)
		r.logger.Debugf("%x paused sending replication messages to %x [%s]", r.id, to, pr)

		//r.send(pb.Message{To: to, Type: pb.MsgSnap, Snapshot: &snapshot})
		return true
	}

	// Send the actual MsgApp otherwise, and update the progress accordingly.
	if err := pr.UpdateOnEntriesSend(len(ents), uint64(payloadsSize(ents)), nextIndex); err != nil {
		r.logger.Panicf("%x: %v", r.id, err)
	}
	// NB: pr has been updated, but we make sure to only use its old values below.
	r.send(pb.Message{
		To:      to,
		Type:    pb.MsgApp,
		Index:   lastIndex,
		LogTerm: lastTerm,
		Entries: ents,
		Commit:  r.raftLog.committed,
	})
	return true
}

// maybeCommit attempts to advance the commit index. Returns true if
// the commit index changed (in which case the caller should call
// r.bcastAppend).
func (r *raft) maybeCommit() bool {
	mci := r.trk.Committed()
	return r.raftLog.maybeCommit(mci, r.Term)
}

func (r *raft) reset(term uint64) {
	if r.Term != term {
		r.Term = term
		r.Vote = None
	}
	r.lead = None

	r.electionElapsed = 0
	r.heartbeatElapsed = 0
	r.resetRandomizedElectionTimeout()

	//r.abortLeaderTransfer()

	r.trk.ResetVotes()
	r.trk.Visit(func(id uint64, pr *tracker.Progress) {
		*pr = tracker.Progress{
			Match: 0,
			Next:  r.raftLog.lastIndex() + 1,
			//Inflights: tracker.NewInflights(r.trk.MaxInflight, r.trk.MaxInflightBytes),
			IsLearner: pr.IsLearner,
		}
		if id == r.id {
			pr.Match = r.raftLog.lastIndex()
		}
	})

	r.pendingConfIndex = 0
	//r.uncommittedSize = 0
	//r.readOnly = newReadOnly(r.readOnly.option)
}

func (r *raft) appendEntry(es ...pb.Entry) (accepted bool) {
	li := r.raftLog.lastIndex()
	for i := range es {
		es[i].Term = r.Term
		es[i].Index = li + 1 + uint64(i)
	}
	// Track the size of this uncommitted proposal.
	//if !r.increaseUncommittedSize(es) {
	//	r.logger.Warningf(
	//		"%x appending new entries to log would exceed uncommitted entry size limit; dropping proposal",
	//		r.id,
	//	)
	//	// Drop the proposal.
	//	return false
	//}
	// use latest "last" index after truncate/append
	li = r.raftLog.append(es...)
	// The leader needs to self-ack the entries just appended once they have
	// been durably persisted (since it doesn't send an MsgApp to itself). This
	// response message will be added to msgsAfterAppend and delivered back to
	// this node after these entries have been written to stable storage. When
	// handled, this is roughly equivalent to:
	//
	//  r.trk.Progress[r.id].MaybeUpdate(e.Index)
	//  if r.maybeCommit() {
	//  	r.bcastAppend()
	//  }
	r.send(pb.Message{To: r.id, Type: pb.MsgAppResp, Index: li})
	return true
}

func (r *raft) applyConfChange(cc pb.ConfChangeV2) pb.ConfState {
	cfg, trk, err := func() (tracker.Config, tracker.ProgressMap, error) {
		changer := confchange.Changer{
			Tracker:   r.trk,
			LastIndex: r.raftLog.lastIndex(),
		}
		//if cc.LeaveJoint() {
		//	return changer.LeaveJoint()
		//} else if autoLeave, ok := cc.EnterJoint(); ok {
		//	return changer.EnterJoint(autoLeave, cc.Changes...)
		//}
		return changer.Simple(cc.Changes...)
	}()

	if err != nil {
		// TODO(tbg): return the error to the caller.
		panic(err)
	}

	return r.switchToConfig(cfg, trk)
}

// switchToConfig reconfigures this node to use the provided configuration. It
// updates the in-memory state and, when necessary, carries out additional
// actions such as reacting to the removal of nodes or changed quorum
// requirements.
//
// The inputs usually result from restoring a ConfState or applying a ConfChange.
func (r *raft) switchToConfig(cfg tracker.Config, trk tracker.ProgressMap) pb.ConfState {
	r.trk.Config = cfg
	r.trk.Progress = trk

	r.logger.Infof("%x switched to configuration %s", r.id, r.trk.Config)
	cs := r.trk.ConfState()
	pr, ok := r.trk.Progress[r.id]

	// Update whether the node itself is a learner, resetting to false when the
	// node is removed.
	r.isLearner = ok && pr.IsLearner

	if (!ok || r.isLearner) && r.state == StateLeader {
		// This node is leader and was removed or demoted, step down if requested.
		//
		// We prevent demotions at the time writing but hypothetically we handle
		// them the same way as removing the leader.
		//
		// TODO(tbg): ask follower with largest Match to TimeoutNow (to avoid
		// interruption). This might still drop some proposals but it's better than
		// nothing.
		if r.stepDownOnRemoval {
			r.becomeFollower(r.Term, None)
		}
		return cs
	}

	// The remaining steps only make sense if this node is the leader and there
	// are other nodes.
	if r.state != StateLeader || len(cs.Voters) == 0 {
		return cs
	}

	if r.maybeCommit() {
		// If the configuration change means that more entries are committed now,
		// broadcast/append to everyone in the updated config.
		r.bcastAppend()
	} else {
		// Otherwise, still probe the newly added replicas; there's no reason to
		// let them wait out a heartbeat interval (or the next incoming
		// proposal).
		r.trk.Visit(func(id uint64, pr *tracker.Progress) {
			if id == r.id {
				return
			}
			r.maybeSendAppend(id, false /* sendIfEmpty */)
		})
	}
	// If the leadTransferee was removed or demoted, abort the leadership transfer.
	//if _, tOK := r.trk.Config.Voters.IDs()[r.leadTransferee]; !tOK && r.leadTransferee != 0 {
	//	r.abortLeaderTransfer()
	//}

	return cs
}

func (r *raft) resetRandomizedElectionTimeout() {
	r.randomizedElectionTimeout = r.electionTimeout + globalRand.Intn(r.electionTimeout)
}
