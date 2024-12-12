package quorum

// JointConfig is a configuration of two groups of (possibly overlapping)
// majority configurations. Decisions require the support of both majorities.
type JointConfig [2]MajorityConfig

// IDs returns a newly initialized map representing the set of voters present
// in the joint configuration.
func (c JointConfig) IDs() map[uint64]struct{} {
	m := map[uint64]struct{}{}
	for _, cc := range c {
		for id := range cc {
			m[id] = struct{}{}
		}
	}
	return m
}
