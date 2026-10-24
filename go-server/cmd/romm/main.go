// Command romm is the single-binary RomM server (Go rewrite).
package main

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/zszwede/romm-go/internal/config"
	"github.com/zszwede/romm-go/internal/httpapi"
	"github.com/zszwede/romm-go/internal/store"
	"github.com/zszwede/romm-go/internal/worker"
)

func main() {
	cfg := config.FromEnv()

	st, err := store.Open(cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPass, cfg.DBName)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	queue := worker.New(ctx, 2, 64)
	scanRoot := filepath.Join(cfg.BasePath, "library", "roms")

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(st, cfg.AuthSecret, queue, scanRoot).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("romm-go %s listening on %s (db %s:%s/%s, scan root %s)",
		httpapi.Version, cfg.Addr, cfg.DBHost, cfg.DBPort, cfg.DBName, scanRoot)
	log.Fatal(srv.ListenAndServe())
}
