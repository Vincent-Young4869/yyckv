package raftpb

import (
	"github.com/gogo/protobuf/proto"
)

// ConfChangeI abstracts over ConfChangeV2 and (legacy) ConfChange to allow
// treating them in a unified manner.
type ConfChangeI interface {
	AsV2() ConfChangeV2
	AsV1() (ConfChange, bool)
}

// AsV2 returns a V2 configuration change carrying out the same operation.
func (c ConfChange) AsV2() ConfChangeV2 {
	return ConfChangeV2{
		Changes: []ConfChangeSingle{{
			Type:   c.Type,
			NodeID: c.NodeID,
		}},
		Context: c.Context,
	}
}

// LeaveJoint is true if the configuration change leaves a joint configuration.
// This is the case if the ConfChangeV2 is zero, with the possible exception of
// the Context field.
func (c ConfChangeV2) LeaveJoint() bool {
	// NB: c is already a copy.
	c.Context = nil
	return proto.Equal(&c, &ConfChangeV2{})
}
