package http

import (
	"bytes"
	"context"
	"errors"
	"go.uber.org/zap"
	"io"
	"runtime"
	"sync"
	raftModel "yyckv/raft/models"
	"yyckv/raft/raftpb"
)

const (
	connPerPipeline = 4
	// pipelineBufSize is the size of pipeline buffer, which helps hold the
	// temporary network latency.
	// The size ensures that pipeline does not drop messages when the network
	// is out of work for less than 1 second in good path.
	pipelineBufSize = 64
)

var errStopped = errors.New("stopped")

type pipeline struct {
	peerID raftModel.ID

	tr     *Transport
	picker *urlPicker
	status *peerStatus
	raft   Raft
	errorc chan error
	// deprecate when we depercate v2 API
	//followerStats *stats.FollowerStats

	msgc chan raftpb.Message
	// wait for the handling routines
	wg    sync.WaitGroup
	stopc chan struct{}
}

func (p *pipeline) start() {
	p.stopc = make(chan struct{})
	p.msgc = make(chan raftpb.Message, pipelineBufSize)
	p.wg.Add(connPerPipeline)
	for i := 0; i < connPerPipeline; i++ {
		go p.handle()
	}

	if p.tr != nil && p.tr.Logger != nil {
		p.tr.Logger.Info(
			"started HTTP pipelining with remote peer",
			zap.String("local-member-id", p.tr.ID.String()),
			zap.String("remote-peer-id", p.peerID.String()),
		)
	}
}

func (p *pipeline) stop() {
	close(p.stopc)
	p.wg.Wait()

	if p.tr != nil && p.tr.Logger != nil {
		p.tr.Logger.Info(
			"stopped HTTP pipelining with remote peer",
			zap.String("local-member-id", p.tr.ID.String()),
			zap.String("remote-peer-id", p.peerID.String()),
		)
	}
}

func (p *pipeline) handle() {
	defer p.wg.Done()

	for {
		select {
		case m := <-p.msgc:
			//start := time.Now()
			err := p.post(raftpb.MustMarshal(&m))
			//end := time.Now()

			if err != nil {
				p.status.deactivate(failureType{source: pipelineMsg, action: "write"}, err.Error())

				//if isMsgApp(m) && p.followerStats != nil {
				//	p.followerStats.Fail()
				//}
				p.raft.ReportUnreachable(m.To)
				//if isMsgSnap(m) {
				//	p.raft.ReportSnapshot(m.To, raft.SnapshotFailure)
				//}
				//sentFailures.WithLabelValues(raftModel.ID(m.To).String()).Inc()
				continue
			}

			p.status.activate()
			//if isMsgApp(m) && p.followerStats != nil {
			//	p.followerStats.Succ(end.Sub(start))
			//}
			//if isMsgSnap(m) {
			//	p.raft.ReportSnapshot(m.To, raft.SnapshotFinish)
			//}
			//sentBytes.WithLabelValues(raftModel.ID(m.To).String()).Add(float64(m.Size()))
		case <-p.stopc:
			return
		}
	}
}

// post POSTs a data payload to a url. Returns nil if the POST succeeds,
// error on any failure.
func (p *pipeline) post(data []byte) (err error) {
	u := p.picker.pick()
	req := createPostRequest(p.tr.Logger, u, RaftPrefix, bytes.NewBuffer(data), "application/protobuf", p.tr.URLs, p.tr.ID, p.tr.ClusterID)

	done := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	go func() {
		select {
		case <-done:
			cancel()
		case <-p.stopc:
			waitSchedule()
			cancel()
		}
	}()

	resp, err := p.tr.pipelineRt.RoundTrip(req)
	done <- struct{}{}
	if err != nil {
		p.picker.unreachable(u)
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		p.picker.unreachable(u)
		return err
	}

	err = checkPostResponse(p.tr.Logger, resp, b, req, p.peerID)
	if err != nil {
		p.picker.unreachable(u)
		// errMemberRemoved is a critical error since a removed member should
		// always be stopped. So we use reportCriticalError to report it to errorc.
		if errors.Is(err, errMemberRemoved) {
			reportCriticalError(err, p.errorc)
		}
		return err
	}

	return nil
}

// waitSchedule waits other goroutines to be scheduled for a while
func waitSchedule() { runtime.Gosched() }
