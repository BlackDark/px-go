package proxy

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A client that half-closes its write side after the request must still
// receive the response. Closing both sockets on EOF would silently drop it.
func TestRelayPreservesResponseAfterClientHalfClose(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()

	served := make(chan struct{})
	go func() {
		conn, err := backend.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Returns once the peer half-closes; the reply must still get out.
		_, _ = io.Copy(io.Discard, conn)
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nhi")
		close(served)
	}()

	upstream, err := net.Dial("tcp", backend.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	go func() {
		client, err := ln.Accept()
		if err != nil {
			return
		}
		Relay(client, upstream, 5*time.Second)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		if err := tc.CloseWrite(); err != nil {
			t.Fatal(err)
		}
	}

	<-served
	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "hi") {
		t.Fatalf("response lost after client half-close, got %q", got)
	}
}

// stubConn is a net.Conn with scripted reads and a configurable write error, so
// relay direction handling can be tested deterministically without TCP timing.
type stubConn struct {
	net.Conn
	in       []byte
	gate     <-chan struct{}
	onWrite  func()
	writeErr error
	closed   bool
	written  bytes.Buffer
}

func (c *stubConn) Read(p []byte) (int, error) {
	if c.gate != nil {
		// Hold the response back until the test has observed the failing write.
		<-c.gate
	}
	if len(c.in) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.in)
	c.in = c.in[n:]
	return n, nil
}

func (c *stubConn) Write(p []byte) (int, error) {
	if c.closed {
		return 0, errors.New("write on closed conn")
	}
	if c.onWrite != nil {
		c.onWrite()
	}
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.written.Write(p)
}

func (c *stubConn) Close() error                     { c.closed = true; return nil }
func (c *stubConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubConn) SetWriteDeadline(time.Time) error { return nil }

var errUpstreamGone = errors.New("upstream gone")

// Upstream can send its response and then go away, so a failing write on the
// client->upstream direction must not destroy the response still being read.
//
// The response is gated until the failing write has been attempted: otherwise
// the response pump can win the race, deliver first, and the test would pass
// even against the bug it is meant to catch.
func TestRelayKeepsResponseWhenUpstreamWriteFails(t *testing.T) {
	const want = "HTTP/1.1 200 OK\r\n\r\nbody"
	attempted := make(chan struct{})
	release := make(chan struct{})

	client := &stubConn{in: []byte("GET / HTTP/1.1\r\n\r\n")}
	upstream := &stubConn{
		in:       []byte(want),
		gate:     release,
		writeErr: errUpstreamGone,
		onWrite:  func() { close(attempted) },
	}

	done := make(chan struct{})
	go func() {
		Relay(client, upstream, time.Second)
		close(done)
	}()

	<-attempted
	close(release)
	<-done

	if got := client.written.String(); got != want {
		t.Fatalf("response lost when upstream write failed: client got %q, want %q", got, want)
	}
}
