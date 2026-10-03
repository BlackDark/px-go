package proxy

import (
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
