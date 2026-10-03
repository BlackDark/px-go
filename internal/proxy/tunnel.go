package proxy

import (
	"io"
	"net"
	"sync"
	"time"
)

const relayBufferSize = 32 * 1024

// closeWrite half-closes the write side so the peer can still deliver the
// response after we stop reading. Plain Close would drop it.
func closeWrite(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
}

func Relay(left, right net.Conn, idle time.Duration) {
	var wg sync.WaitGroup
	pump := func(dst, src net.Conn) {
		defer wg.Done()
		buf := make([]byte, relayBufferSize)
		for {
			_ = src.SetReadDeadline(time.Now().Add(idle))
			n, err := src.Read(buf)
			if n > 0 {
				_ = dst.SetWriteDeadline(time.Now().Add(idle))
				if _, werr := dst.Write(buf[:n]); werr != nil {
					break
				}
			}
			if err != nil {
				if err != io.EOF {
					_ = src.Close()
				}
				break
			}
		}
		closeWrite(dst)
	}
	wg.Add(2)
	go pump(left, right)
	go pump(right, left)
	wg.Wait()
	_ = left.Close()
	_ = right.Close()
}
