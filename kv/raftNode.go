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
	logger "yyckv/raft/log"
	raftModel "yyckv/raft/models"
	"yyckv/raft/raftpb"
	"yyckv/raft/snap"
)

type commit struct {
	data       []string
	applyDoneC chan<- struct{}
}

// A key-value stream backed by raft
type raftNode struct {
	proposeC    <-chan string            // proposed messages (k,v)
	confChangeC <-chan raftpb.ConfChange // proposed cluster config changes
	commitC     chan<- *commit           // entries committed to log (k,v)
	errorC      chan<- error             // errors from raft session

	id      int      // client ID for raft session
	peers   []string // raft peer URLs
	join    bool     // node is joining an existing cluster
	snapdir string   // path to snapshot directory

	confState    raftpb.ConfState
	appliedIndex uint64

	// raft backing for the commit/error channel
	node        raftCore.Node
	raftStorage *raftCore.MemoryStorage

	snapshotter      *snap.Snapshotter
	snapshotterReady chan *snap.Snapshotter // signals when snapshotter is ready

	transport *raftHttp.Transport
	stopc     chan struct{} // signals proposal channel closed
	httpstopc chan struct{} // signals http server to shutdown
	httpdonec chan struct{} // signals http server shutdown complete

	logger *zap.Logger
}

func NewRaftNode(id int, peers []string, join bool, getSnapshot func() ([]byte, error),
	proposeC <-chan string, confChangeC <-chan raftpb.ConfChange,
) (<-chan *commit, <-chan error, <-chan *snap.Snapshotter) {
	commitC := make(chan *commit)
	errorC := make(chan error)

	rn := &raftNode{
		proposeC:    proposeC,
		confChangeC: confChangeC,
		commitC:     commitC,
		errorC:      errorC,
		id:          id,
		peers:       peers,
		join:        join,

		stopc:     make(chan struct{}),
		httpstopc: make(chan struct{}),
		httpdonec: make(chan struct{}),

		logger: logger.CreateZapLogger(),

		snapshotterReady: make(chan *snap.Snapshotter, 1),
	}
	go rn.startRaft()
	return commitC, errorC, rn.snapshotterReady
}

func (rn *raftNode) startRaft() {
	//if !fileutil.Exist(rn.snapdir) {
	//	if err := os.Mkdir(rn.snapdir, 0o750); err != nil {
	//		log.Fatalf("raftexample: cannot create dir for snapshot (%v)", err)
	//	}
	//}
	rn.snapshotter = snap.New(zap.NewExample(), rn.snapdir)
	//
	//oldwal := wal.Exist(rn.waldir)
	oldwal := false
	//rn.wal = rn.replayWAL()
	rn.replayWAL()

	// signal replay has finished
	rn.snapshotterReady <- rn.snapshotter

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
		//LeaderStats: stats.NewLeaderStats(zap.NewExample(), strnonv.Itoa(rn.id)),
		ErrorC: make(chan error),
	}

	rn.transport.Start()
	for i := range rn.peers {
		if i+1 != rn.id {
			rn.transport.AddPeer(raftModel.ID(i+1), []string{rn.peers[i]})
		}
	}

	go rn.serveRaftHttp()
	go rn.serveChannels()
}

func (rn *raftNode) serveChannels() {
	//rn.confState = raftModel.ConfState{}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	// send proposals over raft
	go func() {
		confChangeCount := uint64(0)

		for rn.proposeC != nil && rn.confChangeC != nil {
			select {
			case prop, ok := <-rn.proposeC:
				if !ok {
					rn.proposeC = nil
				} else {
					// blocks until accepted by raft state machine
					rn.node.Propose(context.TODO(), []byte(prop))
				}

			case cc, ok := <-rn.confChangeC:
				if !ok {
					rn.confChangeC = nil
				} else {
					confChangeCount++
					cc.ID = confChangeCount
					rn.node.ProposeConfChange(context.TODO(), cc)
				}
			}
		}
		// client closed channel; shutdown raft if not already
		close(rn.stopc)
	}()

	for {
		select {
		case <-ticker.C:
			rn.node.Tick()
		case rd := <-rn.node.Ready():
			rn.raftStorage.Append(rd.Entries)
			rn.transport.Send(rn.processMessages(rd.Messages))
			applyDoneC, ok := rn.publishEntries(rn.entriesToApply(rd.CommittedEntries))
			if !ok {
				rn.stop()
				return
			}
			rn.maybeTriggerSnapshot(applyDoneC)
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

func (rn *raftNode) serveRaftHttp() {
	url, err := url.Parse(rn.peers[rn.id-1])
	if err != nil {
		log.Fatalf("kvRaftNode: Failed parsing URL (%v)", err)
	}

	ln, err := newStoppableListener(url.Host, rn.httpstopc)
	if err != nil {
		log.Fatalf("kvRaftNode: Failed to listen rafthttp (%v)", err)
	}

	err = (&http.Server{Handler: rn.transport.Handler()}).Serve(ln)
	select {
	case <-rn.httpstopc:
	default:
		log.Fatalf("raftexample: Failed to serve rafthttp (%v)", err)
	}
	close(rn.httpdonec)
}

// When there is a `raftpb.EntryConfChange` after creating the snapshot,
// then the confState included in the snapshot is out of date. so We need
// to update the confState before sending a snapshot to a follower.
func (rn *raftNode) processMessages(ms []raftpb.Message) []raftpb.Message {
	for i := 0; i < len(ms); i++ {
		if ms[i].Type == raftpb.MsgSnap {
			ms[i].Snapshot.Metadata.ConfState = rn.confState
		}
	}
	return ms
}

func (rn *raftNode) maybeTriggerSnapshot(applyDoneC <-chan struct{}) {
	// TODO: implement this
}

func (rn *raftNode) entriesToApply(ents []raftpb.Entry) (nents []raftpb.Entry) {
	if len(ents) == 0 {
		return ents
	}
	firstIdx := ents[0].Index
	if firstIdx > rn.appliedIndex+1 {
		log.Fatalf("first index of committed entry[%d] should <= progress.appliedIndex[%d]+1", firstIdx, rn.appliedIndex)
	}
	if rn.appliedIndex-firstIdx+1 < uint64(len(ents)) {
		nents = ents[rn.appliedIndex-firstIdx+1:]
	}
	return nents
}

// publishEntries writes committed log entries to commit channel and returns
// whether all entries could be published.
func (rn *raftNode) publishEntries(ents []raftpb.Entry) (<-chan struct{}, bool) {
	if len(ents) == 0 {
		return nil, true
	}

	data := make([]string, 0, len(ents))
	for i := range ents {
		switch ents[i].Type {
		case raftpb.EntryNormal:
			if len(ents[i].Data) == 0 {
				// ignore empty messages
				break
			}
			s := string(ents[i].Data)
			data = append(data, s)
		case raftpb.EntryConfChange:
			var cc raftpb.ConfChange
			cc.Unmarshal(ents[i].Data)
			rn.confState = *rn.node.ApplyConfChange(cc)
			switch cc.Type {
			case raftpb.ConfChangeAddNode:
				if len(cc.Context) > 0 {
					rn.transport.AddPeer(raftModel.ID(cc.NodeID), []string{string(cc.Context)})
				}
			case raftpb.ConfChangeRemoveNode:
				if cc.NodeID == uint64(rn.id) {
					log.Println("I've been removed from the cluster! Shutting down.")
					return nil, false
				}
				rn.transport.RemovePeer(raftModel.ID(cc.NodeID))
			}
		}
	}

	var applyDoneC chan struct{}

	if len(data) > 0 {
		applyDoneC = make(chan struct{}, 1)
		select {
		case rn.commitC <- &commit{data, applyDoneC}:
		case <-rn.stopc:
			return nil, false
		}
	}

	// after commit, update appliedIndex
	rn.appliedIndex = ents[len(ents)-1].Index

	return applyDoneC, true
}

// replayWAL replays WAL entries into the raft instance.
// func (rn *raftNode) replayWAL() *wal.WAL {
func (rn *raftNode) replayWAL() {
	log.Printf("replaying WAL of member %d", rn.id)
	//snapshot := rn.loadSnapshot()
	//w := rn.openWAL(snapshot)
	//_, st, ents, err := w.ReadAll()
	//if err != nil {
	//	log.Fatalf("raftexample: failed to read WAL (%v)", err)
	//}
	rn.raftStorage = raftCore.NewMemoryStorage()
	//if snapshot != nil {
	//	rn.raftStorage.ApplySnapshot(*snapshot)
	//}
	//rn.raftStorage.SetHardState(st)

	// append to storage so raft starts at the right place in log
	//rn.raftStorage.Append(ents)

	//return w
}

func (rn *raftNode) writeError(err error) {
	rn.stopHTTP()
	rn.errorC <- err
	close(rn.errorC)
	rn.node.Stop()
}

// stop closes http, closes all channels, and stops raft.
func (rn *raftNode) stop() {
	rn.stopHTTP()
	close(rn.commitC)
	close(rn.errorC)
	rn.node.Stop()
}

func (rn *raftNode) stopHTTP() {
	rn.transport.Stop()
	close(rn.httpstopc)
	<-rn.httpdonec
}

// Implement the transport.Raft interface:

func (rn *raftNode) Process(ctx context.Context, m raftpb.Message) error {
	return rn.node.Step(ctx, m)
}

func (rn *raftNode) IsIDRemoved(_ uint64) bool { return false }

func (rn *raftNode) ReportUnreachable(id uint64) { rn.node.ReportUnreachable(id) }
