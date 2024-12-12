package tracker

import (
	"sort"
	"yyckv/raft/core/quorum"
)

// Config reflects the configuration tracked in a ProgressTracker.
type Config struct {
	Voters quorum.JointConfig

	AutoLeave bool

	Learners map[uint64]struct{}

	LearnersNext map[uint64]struct{}
}

// Clone returns a copy of the Config that shares no memory with the original.
func (c *Config) Clone() Config {
	clone := func(m map[uint64]struct{}) map[uint64]struct{} {
		if m == nil {
			return nil
		}
		mm := make(map[uint64]struct{}, len(m))
		for k := range m {
			mm[k] = struct{}{}
		}
		return mm
	}
	return Config{
		Voters:       quorum.JointConfig{clone(c.Voters[0]), clone(c.Voters[1])},
		Learners:     clone(c.Learners),
		LearnersNext: clone(c.LearnersNext),
	}
}

// ProgressTracker tracks the currently active configuration and the information
// known about the nodes and learners in it. In particular, it tracks the match
// index for each peer which in turn allows reasoning about the committed index.
type ProgressTracker struct {
	Config

	Progress ProgressMap

	Votes map[uint64]bool

	MaxInflight      int
	MaxInflightBytes uint64
}

// Visit invokes the supplied closure for all tracked progresses in stable order.
func (p *ProgressTracker) Visit(f func(id uint64, pr *Progress)) {
	n := len(p.Progress)
	// We need to sort the IDs and don't want to allocate since this is hot code.
	// The optimization here mirrors that in `(MajorityConfig).CommittedIndex`,
	// see there for details.
	var sl [7]uint64
	var ids []uint64
	if len(sl) >= n {
		ids = sl[:n]
	} else {
		ids = make([]uint64, n)
	}
	for id := range p.Progress {
		n--
		ids[n] = id
	}
	insertionSort(ids)
	for _, id := range ids {
		f(id, p.Progress[id])
	}
}

// VoterNodes returns a sorted slice of voters.
func (p *ProgressTracker) VoterNodes() []uint64 {
	m := p.Voters.IDs()
	nodes := make([]uint64, 0, len(m))
	for id := range m {
		nodes = append(nodes, id)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
	return nodes
}

func insertionSort(sl []uint64) {
	a, b := 0, len(sl)
	for i := a + 1; i < b; i++ {
		for j := i; j > a && sl[j] < sl[j-1]; j-- {
			sl[j], sl[j-1] = sl[j-1], sl[j]
		}
	}
}
