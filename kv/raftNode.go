package kv

import (
	"context"
	"go.uber.org/zap"
	"log"
	"net/http"
	"net/url"
	"time"
	raftCore "yyckv/raft/core"
	raftHttp "yyckv/raft/http"
	raftModel "yyckv/raft/models"
	"yyckv/raft/raftpb"
)

// A key-value stream backed by raft
type raftNode struct {
	confChangeC <-chan raftpb.ConfChange // proposed cluster config changes
	errorC      chan<- error             // errors from raft session

	id    int      // client ID for raft session
	peers []string // raft peer URLs
	join  bool     // node is joining an existing cluster

	confState raftpb.ConfState

	// raft backing for the commit/error channel
	node        raftCore.Node
	raftStorage *raftCore.MemoryStorage

	transport *raftHttp.Transport
	stopc     chan struct{} // signals proposal channel closed
	httpstopc chan struct{} // signals http server to shutdown
	httpdonec chan struct{} // signals http server shutdown complete

	logger *zap.Logger
}

func NewRaftNode(id int, peers []string, join bool, confChangeC <-chan raftpb.ConfChange) <-chan error {
	errorC := make(chan error)

	rn := &raftNode{
		confChangeC: confChangeC,
		errorC:      errorC,
		id:          id,
		peers:       peers,
		join:        join,

		stopc:     make(chan struct{}),
		httpstopc: make(chan struct{}),
		httpdonec: make(chan struct{}),

		logger: zap.NewExample(),
	}
	go rn.startRaft()
	return errorC
}

func (rn *raftNode) startRaft() {
	//oldwal := wal.Exist(rn.waldir)
	oldwal := false

	rpeers := make([]raftCore.Peer, len(rn.peers))
	for i := range rpeers {
		// TODO: replcae the "hardcode" with the actual peer ID
		rpeers[i] = raftCore.Peer{ID: uint64(i + 1)}
	}
	c := &raftCore.Config{
		ID:                        uint64(rn.id),
		ElectionTick:              10,
		HeartbeatTick:             1,
		Storage:                   rn.raftStorage,
		MaxSizePerMsg:             1024 * 1024,
		MaxInflightMsgs:           256,
		MaxUncommittedEntriesSize: 1 << 30,
	}

	if oldwal || rn.join {
		rn.node = raftCore.RestartNode(c)
	} else {
		rn.node = raftCore.StartNode(c, rpeers)
	}

	rn.transport = &raftHttp.Transport{
		Logger:    rn.logger,
		ID:        raftModel.ID(rn.id),
		ClusterID: 0x1000,
		Raft:      rn,
		//ServerStats: stats.NewServerStats("", ""),
		//LeaderStats: stats.NewLeaderStats(zap.NewExample(), strconv.Itoa(rn.id)),
		ErrorC: make(chan error),
	}

	rn.transport.Start()
	for i := range rn.peers {
		if i+1 != rn.id {
			rn.transport.AddPeer(raftModel.ID(i+1), []string{rn.peers[i]})
		}
	}

	go rn.serveRaft()
	go rn.serveChannels()
}

func (rn *raftNode) serveChannels() {
	//rn.confState = raftModel.ConfState{}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	go func() {
		for rn.confChangeC != nil {
			select {
			case cc, ok := <-rn.confChangeC:
				if !ok {
					rn.confChangeC = nil
				} else {
					rn.node.ProposeConfChange(context.TODO(), cc)
				}
			}
		}
		close(rn.stopc)
	}()

	for {
		select {
		case <-ticker.C:
			rn.node.Tick()
		case rd := <-rn.node.Ready():
			rn.transport.Send(rd.Messages)
			rn.node.Advance()
		case err := <-rn.transport.ErrorC:
			rn.writeError(err)
			return
		case <-rn.stopc:
			rn.stop()
			return
		}
	}
}

func (rc *raftNode) serveRaft() {
	url, err := url.Parse(rc.peers[rc.id-1])
	if err != nil {
		log.Fatalf("kvRaftNode: Failed parsing URL (%v)", err)
	}

	ln, err := newStoppableListener(url.Host, rc.httpstopc)
	if err != nil {
		log.Fatalf("kvRaftNode: Failed to listen rafthttp (%v)", err)
	}

	err = (&http.Server{Handler: rc.transport.Handler()}).Serve(ln)
	select {
	case <-rc.httpstopc:
	default:
		log.Fatalf("raftexample: Failed to serve rafthttp (%v)", err)
	}
	close(rc.httpdonec)
}

func (rn *raftNode) writeError(err error) {
	rn.stopHTTP()
	rn.errorC <- err
	close(rn.errorC)
	rn.node.Stop()
}

// stop closes http, closes all channels, and stops raft.
func (rc *raftNode) stop() {
	rc.stopHTTP()
	//close(rc.commitC)
	close(rc.errorC)
	rc.node.Stop()
}

func (rc *raftNode) stopHTTP() {
	rc.transport.Stop()
	close(rc.httpstopc)
	<-rc.httpdonec
}

// Implement the transport.Raft interface:

func (rc *raftNode) Process(ctx context.Context, m raftpb.Message) error {
	return rc.node.Step(ctx, m)
}

func (rc *raftNode) IsIDRemoved(_ uint64) bool { return false }

func (rc *raftNode) ReportUnreachable(id uint64) { rc.node.ReportUnreachable(id) }
