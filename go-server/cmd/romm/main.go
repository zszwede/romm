// Command romm is the single-binary RomM server (Go rewrite, Phase 1).
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/config"
	"github.com/zszwede/romm-go/internal/httpapi"
	"github.com/zszwede/romm-go/internal/store"
)

func main() {
	cfg := config.FromEnv()

	st, err := store.Open(cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPass, cfg.DBName)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(st, cfg.AuthSecret).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("romm-go %s listening on %s (db %s:%s/%s)",
		httpapi.Version, cfg.Addr, cfg.DBHost, cfg.DBPort, cfg.DBName)
	log.Fatal(srv.ListenAndServe())
}
