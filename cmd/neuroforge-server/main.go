// Command neuroforge-server runs the NEUROFORGE X control-plane HTTP API that
// backs the Visual Compiler Studio dashboard.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"neuroforge/api"
)

func main() {
	addr := flag.String("addr", envOr("NEUROFORGE_ADDR", ":8080"), "listen address")
	flag.Parse()

	srv := api.NewServer()
	log.Printf("NEUROFORGE X control plane listening on %s", *addr)
	log.Printf("  GET  /api/health")
	log.Printf("  GET  /api/operators")
	log.Printf("  GET  /api/backends")
	log.Printf("  POST /api/compile   {workload, optimize?}")
	log.Printf("  POST /api/run       {workload, optimize?, concurrency?, cache?}")
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
