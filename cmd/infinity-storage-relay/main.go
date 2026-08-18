package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/amaan/infinity-storage/internal/liverelay"
)

func main() {
	listen := flag.String("listen", ":8080", "relay listen address")
	tokenFile := flag.String("token-file", "", "file containing the relay bearer token")
	debug := flag.Bool("debug", false, "log relay requests, statuses, and latency")
	flag.Parse()
	if *tokenFile == "" {
		log.Fatal("infinity-storage-relay: --token-file required")
	}
	content, err := os.ReadFile(*tokenFile)
	if err != nil {
		log.Fatal(err)
	}
	server, err := liverelay.NewServer(strings.TrimSpace(string(content)))
	if err != nil {
		log.Fatal(err)
	}
	var handler http.Handler = server
	if *debug {
		handler = requestLogger{next: server}
	}
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       35 * time.Second,
	}
	fmt.Printf("Infinity Storage live relay listening on %s\n", *listen)
	log.Fatal(httpServer.ListenAndServe())
}

type requestLogger struct {
	next http.Handler
}

func (l requestLogger) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	started := time.Now()
	writer := &statusWriter{ResponseWriter: response, status: http.StatusOK}
	l.next.ServeHTTP(writer, request)
	log.Printf(
		"relay request method=%s path=%s status=%d elapsed_ms=%d",
		request.Method,
		request.URL.Path,
		writer.status,
		time.Since(started).Milliseconds(),
	)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
