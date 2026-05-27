package proxy

import (
	"io"
	"net"
	"sync"
	"time"
)

func Relay(left, right net.Conn, idle time.Duration) {
	var wg sync.WaitGroup
	pump := func(dst, src net.Conn) {
		defer wg.Done()
		buf := make([]byte, 32*1024)
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
		_ = dst.Close()
		_ = src.Close()
	}
	wg.Add(2)
	go pump(left, right)
	go pump(right, left)
	wg.Wait()
}
