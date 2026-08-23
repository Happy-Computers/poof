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
	tokenFile := flag.String("token-file", "", "file containing a development relay bearer token")
	authorityURL := flag.String("authority-url", "", "Infinity Storage API URL that authorizes account libraries")
	debug := flag.Bool("debug", false, "log relay requests, statuses, and latency")
	flag.Parse()
	if (*tokenFile == "" && *authorityURL == "") || (*tokenFile != "" && *authorityURL != "") {
		log.Fatal("infinity-storage-relay: provide exactly one of --token-file or --authority-url")
	}
	var server *liverelay.Server
	var err error
	if *authorityURL != "" {
		authorizer, authorityErr := liverelay.NewHTTPAuthorizer(*authorityURL)
		if authorityErr != nil {
			log.Fatal(authorityErr)
		}
		server, err = liverelay.NewServerWithAuthorizer(authorizer)
	} else {
		content, readErr := os.ReadFile(*tokenFile)
		if readErr != nil {
			log.Fatal(readErr)
		}
		server, err = liverelay.NewServer(strings.TrimSpace(string(content)))
	}
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
