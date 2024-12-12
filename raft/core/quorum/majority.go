package quorum

// MajorityConfig is a set of IDs that uses majority quorums to make decisions.
type MajorityConfig map[uint64]struct{}
