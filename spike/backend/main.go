//go:build spike

// Trivial fixed-body HTTP backend for the Rust-vs-Go proxy spike.
// Usage: go run -tags spike ./spike/backend --listen 127.0.0.1:19080
package main

import (
	"flag"
	"log"
	"net/http"
)

var body = []byte("hello-from-spike-backend-padding")

func main() {
	listen := flag.String("listen", "127.0.0.1:19080", "listen address")
	flag.Parse()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	})
	srv := &http.Server{Addr: *listen, Handler: mux}
	log.Println("backend listening on", *listen)
	log.Fatal(srv.ListenAndServe())
}