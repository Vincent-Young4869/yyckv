package confchange

import (
	"errors"
	"fmt"
	"yyckv/raft/core/quorum"
	"yyckv/raft/core/tracker"
	pb "yyckv/raft/raftpb"
)

type Changer struct {
	Tracker   tracker.ProgressTracker
	LastIndex uint64
}

// Simple carries out a series of configuration changes that (in aggregate)
// mutates the incoming majority config Voters[0] by at most one. This method
// will return an error if that is not the case, if the resulting quorum is
// zero, or if the configuration is in a joint state (i.e. if there is an
// outgoing configuration).
func (c Changer) Simple(ccs ...pb.ConfChangeSingle) (tracker.Config, tracker.ProgressMap, error) {
	cfg, trk, err := c.checkAndCopy()
	if err != nil {
		return c.err(err)
	}
	if joint(cfg) {
		err := errors.New("can't apply simple config change in joint config")
		return c.err(err)
	}
	if err := c.apply(&cfg, trk, ccs...); err != nil {
		return c.err(err)
	}
	if n := symdiff(incoming(c.Tracker.Voters), incoming(cfg.Voters)); n > 1 {
		return tracker.Config{}, nil, errors.New("more than one voter changed without entering joint config")
	}

	return checkAndReturn(cfg, trk)
}

// checkAndCopy copies the tracker's config and progress map (deeply enough for
// the purposes of the Changer) and returns those copies. It returns an error
// if checkInvariants does.
func (c Changer) checkAndCopy() (tracker.Config, tracker.ProgressMap, error) {
	cfg := c.Tracker.Config.Clone()
	trk := tracker.ProgressMap{}

	for id, pr := range c.Tracker.Progress {
		// A shallow copy is enough because we only mutate the Learner field.
		ppr := *pr
		trk[id] = &ppr
	}
	return checkAndReturn(cfg, trk)
}

// checkAndReturn calls checkInvariants on the input and returns either the
// resulting error or the input.
func checkAndReturn(cfg tracker.Config, trk tracker.ProgressMap) (tracker.Config, tracker.ProgressMap, error) {
	if err := checkInvariants(cfg, trk); err != nil {
		return tracker.Config{}, tracker.ProgressMap{}, err
	}
	return cfg, trk, nil
}

// checkInvariants makes sure that the config and progress are compatible with
// each other. This is used to check both what the Changer is initialized with,
// as well as what it returns.
func checkInvariants(cfg tracker.Config, trk tracker.ProgressMap) error {
	// TODO
	return nil
}

// apply a change to the configuration. By convention, changes to voters are
// always made to the incoming majority config Voters[0]. Voters[1] is either
// empty or preserves the outgoing majority configuration while in a joint state.
func (c Changer) apply(cfg *tracker.Config, trk tracker.ProgressMap, ccs ...pb.ConfChangeSingle) error {
	for _, cc := range ccs {
		if cc.NodeID == 0 {
			// etcd replaces the NodeID with zero if it decides (downstream of
			// raft) to not apply a change, so we have to have explicit code
			// here to ignore these.
			continue
		}
		switch cc.Type {
		case pb.ConfChangeAddNode:
			c.makeVoter(cfg, trk, cc.NodeID)
		//case pb.ConfChangeAddLearnerNode:
		//	c.makeLearner(cfg, trk, cc.NodeID)
		case pb.ConfChangeRemoveNode:
			c.remove(cfg, trk, cc.NodeID)
		case pb.ConfChangeUpdateNode:
		default:
			return fmt.Errorf("unexpected conf type %d", cc.Type)
		}
	}
	if len(incoming(cfg.Voters)) == 0 {
		return errors.New("removed all voters")
	}
	return nil
}

// makeVoter adds or promotes the given ID to be a voter in the incoming
// majority config.
func (c Changer) makeVoter(cfg *tracker.Config, trk tracker.ProgressMap, id uint64) {
	pr := trk[id]
	if pr == nil {
		c.initProgress(cfg, trk, id, false /* isLearner */)
		return
	}

	pr.IsLearner = false
	nilAwareDelete(&cfg.Learners, id)
	nilAwareDelete(&cfg.LearnersNext, id)
	incoming(cfg.Voters)[id] = struct{}{}
}

// initProgress initializes a new progress for the given node or learner.
func (c Changer) initProgress(cfg *tracker.Config, trk tracker.ProgressMap, id uint64, isLearner bool) {
	if !isLearner {
		incoming(cfg.Voters)[id] = struct{}{}
	} else {
		nilAwareAdd(&cfg.Learners, id)
	}
	trk[id] = &tracker.Progress{
		// Initializing the Progress with the last index means that the follower
		// can be probed (with the last index).
		//
		// TODO(tbg): seems awfully optimistic. Using the first index would be
		// better. The general expectation here is that the follower has no log
		// at all (and will thus likely need a snapshot), though the app may
		// have applied a snapshot out of band before adding the replica (thus
		// making the first index the better choice).
		Next:  c.LastIndex,
		Match: 0,
		//Inflights: tracker.NewInflights(c.Tracker.MaxInflight, c.Tracker.MaxInflightBytes),
		IsLearner: isLearner,
		// When a node is first added, we should mark it as recently active.
		// Otherwise, CheckQuorum may cause us to step down if it is invoked
		// before the added node has had a chance to communicate with us.
		RecentActive: true,
	}
}

// remove this peer as a voter or learner from the incoming config.
func (c Changer) remove(cfg *tracker.Config, trk tracker.ProgressMap, id uint64) {
	if _, ok := trk[id]; !ok {
		return
	}

	delete(incoming(cfg.Voters), id)
	nilAwareDelete(&cfg.Learners, id)
	nilAwareDelete(&cfg.LearnersNext, id)

	// If the peer is still a voter in the outgoing config, keep the Progress.
	if _, onRight := outgoing(cfg.Voters)[id]; !onRight {
		delete(trk, id)
	}
}

// nilAwareAdd populates a map entry, creating the map if necessary.
func nilAwareAdd(m *map[uint64]struct{}, id uint64) {
	if *m == nil {
		*m = map[uint64]struct{}{}
	}
	(*m)[id] = struct{}{}
}

// nilAwareDelete deletes from a map, nil'ing the map itself if it is empty after.
func nilAwareDelete(m *map[uint64]struct{}, id uint64) {
	if *m == nil {
		return
	}
	delete(*m, id)
	if len(*m) == 0 {
		*m = nil
	}
}

// err returns zero values and an error.
func (c Changer) err(err error) (tracker.Config, tracker.ProgressMap, error) {
	return tracker.Config{}, nil, err
}

// symdiff returns the count of the symmetric difference between the sets of
// uint64s, i.e. len( (l - r) \union (r - l)).
func symdiff(l, r map[uint64]struct{}) int {
	var n int
	pairs := [][2]quorum.MajorityConfig{
		{l, r}, // count elems in l but not in r
		{r, l}, // count elems in r but not in l
	}
	for _, p := range pairs {
		for id := range p[0] {
			if _, ok := p[1][id]; !ok {
				n++
			}
		}
	}
	return n
}

func joint(cfg tracker.Config) bool {
	return len(outgoing(cfg.Voters)) > 0
}

func incoming(voters quorum.JointConfig) quorum.MajorityConfig      { return voters[0] }
func outgoing(voters quorum.JointConfig) quorum.MajorityConfig      { return voters[1] }
func outgoingPtr(voters *quorum.JointConfig) *quorum.MajorityConfig { return &voters[1] }
