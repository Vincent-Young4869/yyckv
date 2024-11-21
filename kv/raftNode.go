package kv

import (
	raftCore "yyckv/raft/core"
	raftHttp "yyckv/raft/http"
	raftModel "yyckv/raft/models"
)

// A key-value stream backed by raft
type raftNode struct {
	confChangeC <-chan raftModel.ConfChange // proposed cluster config changes
	errorC      chan<- error                // errors from raft session

	id    int      // client ID for raft session
	peers []string // raft peer URLs
	join  bool     // node is joining an existing cluster

	confState raftModel.ConfState

	// raft backing for the commit/error channel
	node raftCore.Node

	transport *raftHttp.Transport
	httpstopc chan struct{} // signals http server to shutdown
	httpdonec chan struct{} // signals http server shutdown complete

}

func NewRaftNode(id int, peers []string, join bool, confChangeC <-chan raftModel.ConfChange) <-chan error {
	errorC := make(chan error)

	rn := &raftNode{
		confChangeC: confChangeC,
		errorC:      errorC,
		id:          id,
		peers:       peers,
		join:        join,

		httpstopc: make(chan struct{}),
		httpdonec: make(chan struct{}),
	}
	go rn.startRaft()
	return errorC
}

func (rn *raftNode) startRaft() {
	// TODO
}
