package http

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"
)

type rwTimeoutDialer struct {
	wtimeoutd  time.Duration
	rdtimeoutd time.Duration
	net.Dialer
}

func (d *rwTimeoutDialer) Dial(network, address string) (net.Conn, error) {
	conn, err := d.Dialer.Dial(network, address)
	tconn := &timeoutConn{
		readTimeout:  d.rdtimeoutd,
		writeTimeout: d.wtimeoutd,
		Conn:         conn,
	}
	return tconn, err
}

type timeoutConn struct {
	net.Conn
	writeTimeout time.Duration
	readTimeout  time.Duration
}

func (c timeoutConn) Write(b []byte) (n int, err error) {
	if c.writeTimeout > 0 {
		if err := c.SetWriteDeadline(time.Now().Add(c.writeTimeout)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(b)
}

func (c timeoutConn) Read(b []byte) (n int, err error) {
	if c.readTimeout > 0 {
		if err := c.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(b)
}

// NewTimeoutTransport returns a transport created using the given TLS info.
// If read/write on the created connection blocks longer than its time limit,
// it will return timeout error.
// If read/write timeout is set, transport will not be able to reuse connection.
func NewTimeoutTransport(info TLSInfo, dialtimeoutd, rdtimeoutd, wtimeoutd time.Duration) (*http.Transport, error) {
	tr, err := NewTransport(info, dialtimeoutd)
	if err != nil {
		return nil, err
	}

	if rdtimeoutd != 0 || wtimeoutd != 0 {
		// the timed out connection will timeout soon after it is idle.
		// it should not be put back to http transport as an idle connection for future usage.
		tr.MaxIdleConnsPerHost = -1
	} else {
		// allow more idle connections between peers to avoid unnecessary port allocation.
		tr.MaxIdleConnsPerHost = 1024
	}

	tr.Dial = (&rwTimeoutDialer{
		Dialer: net.Dialer{
			Timeout:   dialtimeoutd,
			KeepAlive: 30 * time.Second,
		},
		rdtimeoutd: rdtimeoutd,
		wtimeoutd:  wtimeoutd,
	}).Dial
	return tr, nil
}

type unixTransport struct{ *http.Transport }

func NewTransport(info TLSInfo, dialtimeoutd time.Duration) (*http.Transport, error) {
	cfg, err := info.ClientConfig()
	if err != nil {
		return nil, err
	}

	var ipAddr net.Addr
	if info.LocalAddr != "" {
		ipAddr, err = net.ResolveTCPAddr("tcp", info.LocalAddr+":0")
		if err != nil {
			return nil, err
		}
	}

	t := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialtimeoutd,
			LocalAddr: ipAddr,
			// value taken from http.DefaultTransport
			KeepAlive: 30 * time.Second,
		}).DialContext,
		// value taken from http.DefaultTransport
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     cfg,
	}

	dialer := &net.Dialer{
		Timeout:   dialtimeoutd,
		KeepAlive: 30 * time.Second,
	}

	dialContext := func(ctx context.Context, net, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", addr)
	}
	tu := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         dialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig:     cfg,
		// Cost of reopening connection on sockets is low, and they are mostly used in testing.
		// Long living unix-transport connections were leading to 'leak' test flakes.
		// Alternatively the returned Transport (t) should override CloseIdleConnections to
		// forward it to 'tu' as well.
		IdleConnTimeout: time.Microsecond,
	}
	ut := &unixTransport{tu}

	t.RegisterProtocol("unix", ut)
	t.RegisterProtocol("unixs", ut)

	return t, nil
}

func (urt *unixTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := *req.URL
	req.URL = &url
	req.URL.Scheme = strings.Replace(req.URL.Scheme, "unix", "http", 1)
	return urt.Transport.RoundTrip(req)
}
