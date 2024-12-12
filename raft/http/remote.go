package http

import (
	"go.uber.org/zap"
	raftModel "yyckv/raft/models"
	"yyckv/raft/raftpb"
)

type remote struct {
	lg       *zap.Logger
	localID  raftModel.ID
	id       raftModel.ID
	status   *peerStatus
	pipeline *pipeline
}

func startRemote(tr *Transport, urls raftModel.URLs, id raftModel.ID) *remote {
	picker := newURLPicker(urls)
	status := newPeerStatus(tr.Logger, tr.ID, id)
	pipeline := &pipeline{
		peerID: id,
		tr:     tr,
		picker: picker,
		status: status,
		raft:   tr.Raft,
		errorc: tr.ErrorC,
	}
	pipeline.start()

	return &remote{
		lg:       tr.Logger,
		localID:  tr.ID,
		id:       id,
		status:   status,
		pipeline: pipeline,
	}
}

func (g *remote) send(m raftpb.Message) {
	select {
	case g.pipeline.msgc <- m:
	default:
		if g.status.isActive() {
			if g.lg != nil {
				g.lg.Warn(
					"dropped internal Raft message since sending buffer is full (overloaded network)",
					zap.String("message-type", m.Type.String()),
					zap.String("local-member-id", g.localID.String()),
					zap.String("from", raftModel.ID(m.From).String()),
					zap.String("remote-peer-id", g.id.String()),
					zap.Bool("remote-peer-active", g.status.isActive()),
				)
			}
		} else {
			if g.lg != nil {
				g.lg.Warn(
					"dropped Raft message since sending buffer is full (overloaded network)",
					zap.String("message-type", m.Type.String()),
					zap.String("local-member-id", g.localID.String()),
					zap.String("from", raftModel.ID(m.From).String()),
					zap.String("remote-peer-id", g.id.String()),
					zap.Bool("remote-peer-active", g.status.isActive()),
				)
			}
		}
		//sentFailures.WithLabelValues(raftModel.ID(m.To).String()).Inc()
	}
}

func (g *remote) stop() {
	g.pipeline.stop()
}

func (g *remote) Pause() {
	g.stop()
}

func (g *remote) Resume() {
	g.pipeline.start()
}
