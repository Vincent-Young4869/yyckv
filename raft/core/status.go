package core

import (
	"yyckv/raft/core/tracker"
	pb "yyckv/raft/raftpb"
)

type Status struct {
	BasicStatus
	Config   tracker.Config
	Progress map[uint64]tracker.Progress
}

// BasicStatus contains basic information about the Raft peer. It does not allocate.
type BasicStatus struct {
	ID uint64

	pb.HardState
	SoftState

	Applied uint64

	LeadTransferee uint64
}

func getProgressCopy(r *raft) map[uint64]tracker.Progress {
	m := make(map[uint64]tracker.Progress)
	r.trk.Visit(func(id uint64, pr *tracker.Progress) {
		p := *pr
		//p.Inflights = pr.Inflights.Clone()
		pr = nil

		m[id] = p
	})
	return m
}

func getBasicStatus(r *raft) BasicStatus {
	s := BasicStatus{
		ID:             r.id,
		LeadTransferee: r.leadTransferee,
	}
	//s.HardState = r.hardState()
	//s.SoftState = r.softState()
	s.Applied = r.raftLog.applied
	return s
}

func getStatus(r *raft) Status {
	var s Status
	s.BasicStatus = getBasicStatus(r)
	if s.RaftState == StateLeader {
		s.Progress = getProgressCopy(r)
	}
	s.Config = r.trk.Config.Clone()
	return s
}
