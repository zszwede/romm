// Command romm is the single-binary RomM server (Go rewrite).
package main

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/zszwede/romm-go/internal/achievements"
	"github.com/zszwede/romm-go/internal/config"
	"github.com/zszwede/romm-go/internal/downloadsvc"
	"github.com/zszwede/romm-go/internal/httpapi"
	"github.com/zszwede/romm-go/internal/metadata"
	"github.com/zszwede/romm-go/internal/oidc"
	"github.com/zszwede/romm-go/internal/store"
	"github.com/zszwede/romm-go/internal/virtual"
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
	if cfg.LibraryRomsDir != "" {
		scanRoot = cfg.LibraryRomsDir
	}

	var provider metadata.Provider
	if cfg.IGDBClientID != "" && cfg.IGDBClientSecret != "" {
		provider = metadata.NewIGDB(cfg.IGDBClientID, cfg.IGDBClientSecret)
	}

	var ach httpapi.AchievementService
	if cfg.RAAPIKey != "" {
		ach = achievements.NewClient(cfg.RAAPIKey)
	}

	var oidcProvider *oidc.Provider
	var oidcUsers oidc.UserStore
	if cfg.OIDC.Enabled {
		oidcProvider = oidc.NewProvider(oidc.Config{
			Enabled:           true,
			AllowRegistration: cfg.OIDC.AllowRegistration,
			ClientID:          cfg.OIDC.ClientID,
			ClientSecret:      cfg.OIDC.ClientSecret,
			RedirectURI:       cfg.OIDC.RedirectURI,
			MetadataURL:       cfg.OIDC.MetadataURL,
			ProviderURL:       cfg.OIDC.ProviderURL,
			UsernameAttribute: cfg.OIDC.UsernameAttribute,
			ClaimRoles:        cfg.OIDC.ClaimRoles,
			RoleViewer:        cfg.OIDC.RoleViewer,
			RoleEditor:        cfg.OIDC.RoleEditor,
			RoleAdmin:         cfg.OIDC.RoleAdmin,
		})
		oidcUsers = st
		log.Printf("OIDC login enabled (client %s)", cfg.OIDC.ClientID)
	}

	var virt httpapi.VirtualService
	switch {
	case cfg.DownloaderConfig != "":
		svc, err := downloadsvc.LoadAndStart(cfg.DownloaderConfig, st)
		if err != nil {
			log.Fatalf("downloader: %v", err)
		}
		defer svc.Shutdown()
		virt = svc
		log.Printf("virtual downloads: in-process (config %s)", cfg.DownloaderConfig)
	case cfg.MergeServiceURL != "":
		virt = virtual.NewClient(cfg.MergeServiceURL, cfg.MergeServiceToken)
		log.Printf("virtual downloads: dispatch to %s", cfg.MergeServiceURL)
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Deps{
			Store:        st,
			AuthSecret:   cfg.AuthSecret,
			Queue:        queue,
			ScanRoot:     scanRoot,
			Metadata:     provider,
			Virtual:      virt,
			Achievements: ach,
			WebRoot:       cfg.WebRoot,
			AssetsRoot:    filepath.Join(cfg.BasePath, "assets"),
			ResourcesRoot: filepath.Join(cfg.BasePath, "resources"),
			OIDC:         oidcProvider,
			OIDCUsers:    oidcUsers,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("romm-go %s listening on %s (db %s:%s/%s, scan root %s)",
		httpapi.Version, cfg.Addr, cfg.DBHost, cfg.DBPort, cfg.DBName, scanRoot)
	log.Fatal(srv.ListenAndServe())
}
