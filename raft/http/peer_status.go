package http

import (
	"errors"
	"fmt"
	"go.uber.org/zap"
	"sync"
	"time"
	logger "yyckv/raft/log"
	raftModel "yyckv/raft/models"
)

type failureType struct {
	source string
	action string
}

type peerStatus struct {
	lg     *zap.Logger
	local  raftModel.ID
	id     raftModel.ID
	mu     sync.Mutex // protect variables below
	active bool
	since  time.Time
}

func newPeerStatus(lg *zap.Logger, local, id raftModel.ID) *peerStatus {
	if lg == nil {
		lg = logger.CreateZapLogger()
	}
	return &peerStatus{lg: lg, local: local, id: id}
}

func (s *peerStatus) activate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		s.lg.Info("peer became active", zap.String("peer-id", s.id.String()))
		s.active = true
		s.since = time.Now()

		//activePeers.WithLabelValues(s.local.String(), s.id.String()).Inc()
	}
}

func (s *peerStatus) deactivate(failure failureType, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := fmt.Sprintf("failed to %s %s on %s (%s)", failure.action, s.id, failure.source, reason)
	if s.active {
		s.lg.Warn("peer became inactive (message send to peer failed)", zap.String("peer-id", s.id.String()), zap.Error(errors.New(msg)))
		s.active = false
		s.since = time.Time{}

		//activePeers.WithLabelValues(s.local.String(), s.id.String()).Dec()
		//disconnectedPeers.WithLabelValues(s.local.String(), s.id.String()).Inc()
		return
	}

	if s.lg != nil {
		s.lg.Debug("peer deactivated again", zap.String("peer-id", s.id.String()), zap.Error(errors.New(msg)))
	}
}

func (s *peerStatus) isActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

func (s *peerStatus) activeSince() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.since
}
