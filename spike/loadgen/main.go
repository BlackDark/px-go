//go:build spike

// Fixed-count, fixed-concurrency HTTP client for the Rust-vs-Go proxy spike.
// Deliberately raw-socket and allocation-light so the client itself is not the
// bottleneck on this 3-core box. Reports its own CPU time so the reader can
// check how much headroom the client had.
//
// Usage:
//	go run -tags spike ./spike/loadgen -n 100000 -c 64 \
//	  -proxy 127.0.0.1:18080 -target http://127.0.0.1:29701/hello
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func cpuTicks() float64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	// utime and stime are fields 14 and 15 (1-indexed); comm may contain spaces.
	s := string(b)
	if i := strings.LastIndex(s, ")"); i >= 0 {
		s = s[i+2:]
	}
	f := strings.Fields(s)
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseFloat(f[11], 64)
	s2, _ := strconv.ParseFloat(f[12], 64)
	return u + s2
}

func main() {
	n := flag.Int("n", 100000, "total requests")
	conc := flag.Int("c", 64, "concurrency")
	proxy := flag.String("proxy", "127.0.0.1:18080", "proxy address")
	target := flag.String("target", "http://127.0.0.1:29701/hello", "absolute target URL")
	flag.Parse()

	var ok, fail int64
	var wg sync.WaitGroup
	cpu0 := cpuTicks()
	start := time.Now()
	per := *n / *conc
	extra := *n % *conc
	req := "GET " + *target + " HTTP/1.1\r\nHost: 127.0.0.1:19080\r\nConnection: keep-alive\r\n\r\n"

	for i := 0; i < *conc; i++ {
		count := per
		if i < extra {
			count++
		}
		if count == 0 {
			continue
		}
		wg.Add(1)
		go func(count int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", *proxy)
			if err != nil {
				atomic.AddInt64(&fail, int64(count))
				fmt.Fprintln(os.Stderr, "dial:", err)
				return
			}
			defer conn.Close()
			if tc, isTCP := conn.(*net.TCPConn); isTCP {
				_ = tc.SetNoDelay(true)
			}
			br := bufio.NewReaderSize(conn, 8192)
			for k := 0; k < count; k++ {
				if _, err := io.WriteString(conn, req); err != nil {
					atomic.AddInt64(&fail, int64(count-k))
					return
				}
				if err := readResp(br); err != nil {
					atomic.AddInt64(&fail, int64(count-k))
					fmt.Fprintln(os.Stderr, "req err:", err)
					return
				}
				atomic.AddInt64(&ok, 1)
			}
		}(count)
	}
	wg.Wait()
	el := time.Since(start)
	cpu := cpuTicks() - cpu0
	done := float64(atomic.LoadInt64(&ok))
	fmt.Printf("ok=%d fail=%d elapsed_ms=%.1f rps=%.1f client_cpu_ms=%.1f\n",
		int64(done), atomic.LoadInt64(&fail), float64(el.Microseconds())/1000,
		done/el.Seconds(), cpu*10)
}

// readResp consumes exactly one keep-alive response. The spike backend always
// sets Content-Length, so no chunked handling is needed.
func readResp(br *bufio.Reader) error {
	clen := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if k, v, found := strings.Cut(line, ":"); found &&
			strings.EqualFold(strings.TrimSpace(k), "content-length") {
			clen, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return err
			}
		}
	}
	if clen < 0 {
		return fmt.Errorf("response without content-length")
	}
	_, err := io.CopyN(io.Discard, br, int64(clen))
	return err
}