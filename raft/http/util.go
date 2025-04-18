package http

import (
	"fmt"
	"github.com/coreos/go-semver/semver"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	raftModel "yyckv/raft/models"
)

var (
	errMemberRemoved  = fmt.Errorf("the member has been permanently removed from the cluster")
	errMemberNotFound = fmt.Errorf("member not found")
)

// NewRoundTripper returns a roundTripper used to send requests
// to rafthttp listener of remote peers.
func NewRoundTripper(tlsInfo TLSInfo, dialTimeout time.Duration) (http.RoundTripper, error) {
	// It uses timeout transport to pair with remote timeout listeners.
	// It sets no read/write timeout, because message in requests may
	// take long time to write out before reading out the response.
	return NewTimeoutTransport(tlsInfo, dialTimeout, 0, 0)
}

// newStreamRoundTripper returns a roundTripper used to send stream requests
// to rafthttp listener of remote peers.
// Read/write timeout is set for stream roundTripper to promptly
// find out broken status, which minimizes the number of messages
// sent on broken connection.
func newStreamRoundTripper(tlsInfo TLSInfo, dialTimeout time.Duration) (http.RoundTripper, error) {
	return NewTimeoutTransport(tlsInfo, dialTimeout, ConnReadTimeout, ConnWriteTimeout)
}

// createPostRequest creates a HTTP POST request that sends raft message.
func createPostRequest(lg *zap.Logger, u url.URL, path string, body io.Reader, ct string, urls raftModel.URLs, from, cid raftModel.ID) *http.Request {
	uu := u
	uu.Path = path
	req, err := http.NewRequest(http.MethodPost, uu.String(), body)
	if err != nil {
		if lg != nil {
			lg.Panic("unexpected new request error", zap.Error(err))
		}
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Server-From", from.String())
	//req.Header.Set("X-Server-Version", version.Version)
	//req.Header.Set("X-Min-Cluster-Version", version.MinClusterVersion)
	req.Header.Set("X-Etcd-Cluster-ID", cid.String())
	setPeerURLsHeader(req, urls)

	return req
}

// checkPostResponse checks the response of the HTTP POST request that sends
// raft message.
func checkPostResponse(lg *zap.Logger, resp *http.Response, body []byte, req *http.Request, to raftModel.ID) error {
	switch resp.StatusCode {
	case http.StatusPreconditionFailed:
		switch strings.TrimSuffix(string(body), "\n") {
		case errIncompatibleVersion.Error():
			if lg != nil {
				lg.Error(
					"request sent was ignored by peer",
					zap.String("remote-peer-id", to.String()),
				)
			}
			return errIncompatibleVersion
		case ErrClusterIDMismatch.Error():
			if lg != nil {
				lg.Error(
					"request sent was ignored due to cluster ID mismatch",
					zap.String("remote-peer-id", to.String()),
					zap.String("remote-peer-cluster-id", resp.Header.Get("X-Etcd-Cluster-ID")),
					zap.String("local-member-cluster-id", req.Header.Get("X-Etcd-Cluster-ID")),
				)
			}
			return ErrClusterIDMismatch
		default:
			return fmt.Errorf("unhandled error %q when precondition failed", string(body))
		}
	case http.StatusForbidden:
		return errMemberRemoved
	case http.StatusNoContent:
		return nil
	default:
		return fmt.Errorf("unexpected http status %s while posting to %q", http.StatusText(resp.StatusCode), req.URL.String())
	}
}

// reportCriticalError reports the given error through sending it into
// the given error channel.
// If the error channel is filled up when sending error, it drops the error
// because the fact that error has happened is reported, which is
// good enough.
func reportCriticalError(err error, errc chan<- error) {
	select {
	case errc <- err:
	default:
	}
}

// compareMajorMinorVersion returns an integer comparing two versions based on
// their major and minor version. The result will be 0 if a==b, -1 if a < b,
// and 1 if a > b.
func compareMajorMinorVersion(a, b *semver.Version) int {
	na := &semver.Version{Major: a.Major, Minor: a.Minor}
	nb := &semver.Version{Major: b.Major, Minor: b.Minor}
	switch {
	case na.LessThan(*nb):
		return -1
	case nb.LessThan(*na):
		return 1
	default:
		return 0
	}
}

// serverVersion returns the server version from the given header.
func serverVersion(h http.Header) *semver.Version {
	verStr := h.Get("X-Server-Version")
	// backward compatibility with etcd 2.0
	if verStr == "" {
		verStr = "2.0.0"
	}
	return semver.Must(semver.NewVersion(verStr))
}

// serverVersion returns the min cluster version from the given header.
func minClusterVersion(h http.Header) *semver.Version {
	verStr := h.Get("X-Min-Cluster-Version")
	// backward compatibility with etcd 2.0
	if verStr == "" {
		verStr = "2.0.0"
	}
	return semver.Must(semver.NewVersion(verStr))
}

// checkVersionCompatibility checks whether the given version is compatible
// with the local version.
func checkVersionCompatibility(name string, server, minCluster *semver.Version) (
	localServer *semver.Version,
	localMinCluster *semver.Version,
	err error) {
	//localServer = semver.Must(semver.NewVersion(version.Version))
	//localMinCluster = semver.Must(semver.NewVersion(version.MinClusterVersion))
	if compareMajorMinorVersion(server, localMinCluster) == -1 {
		return localServer, localMinCluster, fmt.Errorf("remote version is too low: remote[%s]=%s, local=%s", name, server, localServer)
	}
	if compareMajorMinorVersion(minCluster, localServer) == 1 {
		return localServer, localMinCluster, fmt.Errorf("local version is too low: remote[%s]=%s, local=%s", name, server, localServer)
	}
	return localServer, localMinCluster, nil
}

// setPeerURLsHeader reports local urls for peer discovery
func setPeerURLsHeader(req *http.Request, urls raftModel.URLs) {
	if urls == nil {
		// often not set in unit tests
		return
	}
	peerURLs := make([]string, urls.Len())
	for i := range urls {
		peerURLs[i] = urls[i].String()
	}
	req.Header.Set("X-PeerURLs", strings.Join(peerURLs, ","))
}

// addRemoteFromRequest adds a remote peer according to an http request header
func addRemoteFromRequest(tr Transporter, r *http.Request) {
	if from, err := raftModel.IDFromString(r.Header.Get("X-Server-From")); err == nil {
		if urls := r.Header.Get("X-PeerURLs"); urls != "" {
			tr.AddRemote(from, strings.Split(urls, ","))
		}
	}
}
