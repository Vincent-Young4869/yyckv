package http

import (
	"context"
	"github.com/xiang90/probing"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"net/http"
	"sync"
	"time"
	raftModel "yyckv/raft/models"
	"yyckv/raft/raftpb"
)

type Raft interface {
	Process(ctx context.Context, m raftpb.Message) error
	IsIDRemoved(id uint64) bool
	ReportUnreachable(id uint64)
	//ReportSnapshot(id uint64, status raft.SnapshotStatus)
}

type Transporter interface {
	// Start starts the given Transporter.
	// Start MUST be called before calling other functions in the interface.
	Start() error
	// Handler returns the HTTP handler of the transporter.
	// A transporter HTTP handler handles the HTTP requests
	// from remote peers.
	// The handler MUST be used to handle RaftPrefix(/raft)
	// endpoint.
	Handler() http.Handler
	// Send sends out the given messages to the remote peers.
	// Each message has a To field, which is an id that maps
	// to an existing peer in the transport.
	// If the id cannot be found in the transport, the message
	// will be ignored.
	Send(m []raftpb.Message)
	// SendSnapshot sends out the given snapshot message to a remote peer.
	// The behavior of SendSnapshot is similar to Send.
	//SendSnapshot(m snap.Message)

	// AddRemote adds a remote with given peer urls into the transport.
	// A remote helps newly joined member to catch up the progress of cluster,
	// and will not be used after that.
	// It is the caller's responsibility to ensure the urls are all valid,
	// or it panics.
	AddRemote(id raftModel.ID, urls []string)
	// AddPeer adds a peer with given peer urls into the transport.
	// It is the caller's responsibility to ensure the urls are all valid,
	// or it panics.
	// Peer urls are used to connect to the remote peer.
	AddPeer(id raftModel.ID, urls []string)
	// RemovePeer removes the peer with given id.
	RemovePeer(id raftModel.ID)
	// RemoveAllPeers removes all the existing peers in the transport.
	RemoveAllPeers()
	// UpdatePeer updates the peer urls of the peer with the given id.
	// It is the caller's responsibility to ensure the urls are all valid,
	// or it panics.
	UpdatePeer(id raftModel.ID, urls []string)
	// ActiveSince returns the time that the connection with the peer
	// of the given id becomes active.
	// If the connection is active since peer was added, it returns the adding time.
	// If the connection is currently inactive, it returns zero time.
	ActiveSince(id raftModel.ID) time.Time
	// ActivePeers returns the number of active peers.
	ActivePeers() int
	// Stop closes the connections and stops the transporter.
	Stop()
}

type Transport struct {
	Logger *zap.Logger

	DialTimeout time.Duration // maximum duration before timing out dial of the request
	// DialRetryFrequency defines the frequency of streamReader dial retrial attempts;
	// a distinct rate limiter is created per every peer (default value: 10 events/sec)
	DialRetryFrequency rate.Limit

	TLSInfo TLSInfo // TLS information used when creating connection

	ID        raftModel.ID   // local member ID
	URLs      raftModel.URLs // local peer URLs
	ClusterID raftModel.ID   // raft cluster ID for request validation
	Raft      Raft           // raft state machine, to which the Transport forwards received messages and reports status

	// ErrorC is used to report detected critical errors, e.g.,
	// the member has been permanently removed from the cluster
	// When an error is received from ErrorC, user should stop raft state
	// machine and thus stop the Transport.
	ErrorC chan error

	streamRt   http.RoundTripper // roundTripper used by streams
	pipelineRt http.RoundTripper // roundTripper used by pipelines

	mu      sync.RWMutex             // protect the remote and peer map
	remotes map[raftModel.ID]*remote // remotes map that helps newly joined member to catch up
	peers   map[raftModel.ID]Peer    // peers map

	pipelineProber probing.Prober
	streamProber   probing.Prober
}

func (t *Transport) Get(id raftModel.ID) Peer {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.peers[id]
}

func (t *Transport) Start() error {
	var err error
	t.streamRt, err = newStreamRoundTripper(t.TLSInfo, t.DialTimeout)
	if err != nil {
		return err
	}
	t.pipelineRt, err = NewRoundTripper(t.TLSInfo, t.DialTimeout)
	if err != nil {
		return err
	}
	t.remotes = make(map[raftModel.ID]*remote)
	t.peers = make(map[raftModel.ID]Peer)
	t.pipelineProber = probing.NewProber(t.pipelineRt)
	t.streamProber = probing.NewProber(t.streamRt)

	// If client didn't provide dial retry frequency, use the default
	// (100ms backoff between attempts to create a new stream),
	// so it doesn't bring too much overhead when retry.
	if t.DialRetryFrequency == 0 {
		t.DialRetryFrequency = rate.Every(100 * time.Millisecond)
	}
	return nil
}

func (t *Transport) Handler() http.Handler {
	pipelineHandler := newPipelineHandler(t, t.Raft, t.ClusterID)
	streamHandler := newStreamHandler(t, t, t.Raft, t.ID, t.ClusterID)
	//snapHandler := newSnapshotHandler(t, t.Raft, t.Snapshotter, t.ClusterID)
	mux := http.NewServeMux()
	mux.Handle(RaftPrefix, pipelineHandler)
	mux.Handle(RaftStreamPrefix+"/", streamHandler)
	//mux.Handle(RaftSnapshotPrefix, snapHandler)
	mux.Handle(ProbingPrefix, probing.NewHandler())
	return mux
}

func (t *Transport) AddPeer(id raftModel.ID, us []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.peers == nil {
		panic("transport stopped")
	}
	if _, ok := t.peers[id]; ok {
		return
	}
	urls, err := raftModel.NewURLs(us)
	if err != nil {
		if t.Logger != nil {
			t.Logger.Panic("failed NewURLs", zap.Strings("urls", us), zap.Error(err))
		}
	}
	//fs := t.LeaderStats.Follower(id.String())
	t.peers[id] = startPeer(t, urls, id)
	//addPeerToProber(t.Logger, t.pipelineProber, id.String(), us, RoundTripperNameSnapshot, rttSec)
	//addPeerToProber(t.Logger, t.streamProber, id.String(), us, RoundTripperNameRaftMessage, rttSec)

	if t.Logger != nil {
		t.Logger.Info(
			"added remote peer",
			zap.String("local-member-id", t.ID.String()),
			zap.String("remote-peer-id", id.String()),
			zap.Strings("remote-peer-urls", us),
		)
	}
}

// add remaining methods for transport so that it implements Transporter interface
func (t *Transport) Send(msgs []raftpb.Message) {
	for _, m := range msgs {
		if m.To == 0 {
			// ignore intentionally dropped message
			continue
		}
		to := raftModel.ID(m.To)

		t.mu.RLock()
		p, pok := t.peers[to]
		g, rok := t.remotes[to]
		t.mu.RUnlock()

		if pok {
			if isMsgApp(m) {
				//t.ServerStats.SendAppendReq(m.Size())
			}
			p.send(m)
			continue
		}

		if rok {
			g.send(m)
			continue
		}

		if t.Logger != nil {
			t.Logger.Debug(
				"ignored message send request; unknown remote peer target",
				zap.String("type", m.Type.String()),
				zap.String("unknown-target-peer-id", to.String()),
			)
		}
	}
}

func (t *Transport) AddRemote(id raftModel.ID, us []string) {
	// TODO
}

func (t *Transport) RemovePeer(id raftModel.ID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.removePeer(id)
}

func (t *Transport) RemoveAllPeers() {
	// TODO
}

func (t *Transport) removePeer(id raftModel.ID) {
	// TODO
}

func (t *Transport) UpdatePeer(id raftModel.ID, us []string) {
	// TODO
}

func (t *Transport) ActiveSince(id raftModel.ID) time.Time {
	// TODO
	return time.Time{}
}

func (t *Transport) ActivePeers() int {
	// TODO
	return 0
}

func (t *Transport) Stop() {
	// TODO
}
