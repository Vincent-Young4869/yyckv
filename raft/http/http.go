package http

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"io"
	"net/http"
	"path"
	raftModel "yyckv/raft/models"
	"yyckv/raft/raftpb"
	"yyckv/raft/utils"
)

const (
	// connReadLimitByte limits the number of bytes
	// a single read can read out.
	//
	// 64KB should be large enough for not causing
	// throughput bottleneck as well as small enough
	// for not causing a read timeout.
	connReadLimitByte = 64 * 1024
)

var (
	RaftPrefix         = "/raft"
	ProbingPrefix      = path.Join(RaftPrefix, "probing")
	RaftStreamPrefix   = path.Join(RaftPrefix, "stream")
	RaftSnapshotPrefix = path.Join(RaftPrefix, "snapshot")

	errIncompatibleVersion = errors.New("incompatible version")
	ErrClusterIDMismatch   = errors.New("cluster ID mismatch")
)

type peerGetter interface {
	Get(id raftModel.ID) Peer
}

type writerToResponse interface {
	WriteTo(w http.ResponseWriter)
}

type pipelineHandler struct {
	lg      *zap.Logger
	localID raftModel.ID
	tr      Transporter
	r       Raft
	cid     raftModel.ID
}

// newPipelineHandler returns a handler for handling raft messages
// from pipeline for RaftPrefix.
//
// The handler reads out the raft message from request body,
// and forwards it to the given raft state machine for processing.
func newPipelineHandler(t *Transport, r Raft, cid raftModel.ID) http.Handler {
	h := &pipelineHandler{
		lg:      t.Logger,
		localID: t.ID,
		tr:      t,
		r:       r,
		cid:     cid,
	}
	if h.lg == nil {
		h.lg = zap.NewNop()
	}
	return h
}

func (h *pipelineHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("X-Etcd-Cluster-ID", h.cid.String())

	//if err := checkClusterCompatibilityFromHeader(h.lg, h.localID, r.Header, h.cid); err != nil {
	//	http.Error(w, err.Error(), http.StatusPreconditionFailed)
	//	return
	//}

	addRemoteFromRequest(h.tr, r)

	// Limit the data size that could be read from the request body, which ensures that read from
	// connection will not time out accidentally due to possible blocking in underlying implementation.
	limitedr := utils.NewLimitedBufferReader(r.Body, connReadLimitByte)
	b, err := io.ReadAll(limitedr)
	if err != nil {
		h.lg.Warn(
			"failed to read Raft message",
			zap.String("local-member-id", h.localID.String()),
			zap.Error(err),
		)
		http.Error(w, "error reading raft message", http.StatusBadRequest)
		//recvFailures.WithLabelValues(r.RemoteAddr).Inc()
		return
	}

	var m raftpb.Message
	if err := m.Unmarshal(b); err != nil {
		h.lg.Warn(
			"failed to unmarshal Raft message",
			zap.String("local-member-id", h.localID.String()),
			zap.Error(err),
		)
		http.Error(w, "error unmarshalling raft message", http.StatusBadRequest)
		//recvFailures.WithLabelValues(r.RemoteAddr).Inc()
		return
	}

	//receivedBytes.WithLabelValues(types.ID(m.From).String()).Add(float64(len(b)))

	if err := h.r.Process(context.TODO(), m); err != nil {
		var writerErr writerToResponse
		switch {
		case errors.As(err, &writerErr):
			writerErr.WriteTo(w)
		default:
			h.lg.Warn(
				"failed to process Raft message",
				zap.String("local-member-id", h.localID.String()),
				zap.Error(err),
			)
			http.Error(w, "error processing raft message", http.StatusInternalServerError)
			w.(http.Flusher).Flush()
			// disconnect the http stream
			panic(err)
		}
		return
	}

	// Write StatusNoContent header after the message has been processed by
	// raft, which facilitates the client to report MsgSnap status.
	w.WriteHeader(http.StatusNoContent)
}

type streamHandler struct {
	lg         *zap.Logger
	tr         *Transport
	peerGetter peerGetter
	r          Raft
	id         raftModel.ID
	cid        raftModel.ID
}

func newStreamHandler(t *Transport, pg peerGetter, r Raft, id, cid raftModel.ID) http.Handler {
	h := &streamHandler{
		lg:         t.Logger,
		tr:         t,
		peerGetter: pg,
		r:          r,
		id:         id,
		cid:        cid,
	}
	if h.lg == nil {
		h.lg = zap.NewNop()
	}
	return h
}

func (h *streamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// TODO
}
