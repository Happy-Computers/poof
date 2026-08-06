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
	httpServer := &http.Server{
		Addr:              *listen,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       35 * time.Second,
	}
	fmt.Printf("Infinity Storage live relay listening on %s\n", *listen)
	log.Fatal(httpServer.ListenAndServe())
}
