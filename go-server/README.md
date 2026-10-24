# romm-go — single-binary RomM (Go rewrite)

Phase 1 scaffold. Reads the EXISTING MariaDB schema (no migration), serves a
heartbeat + read-only roms list + an embedded static shell. One static binary;
only dependency at runtime is MariaDB + the filesystem.

## Run
Uses the same env vars as the Python container:

    DB_HOST=192.168.1.201 DB_USER=romm-user DB_PASSWD=... DB_NAME=romm \
    ROMM_GO_ADDR=:8088 go run ./cmd/romm

Endpoints: `GET /api/heartbeat`, `GET /api/roms?platform_id=&limit=`.

## Roadmap
1. ✅ skeleton + DB + static serving + heartbeat/roms (read-only)
2. core write API (roms/platforms/collections, rom_user) + JWT auth compatible
   with the current `ROMM_AUTH_SECRET_KEY`
3. scanner + native hashing + ES-DE gamelists + in-process worker queue (drops Redis)
4. metadata providers (IGDB, ScreenScraper, Hasheous first)
5. absorb RommDownloader (virtual-download dispatch/queue/merge) into this binary
6. long tail (saves/states, play sessions, streaming, OIDC) — only what's used
Frontend stays the built Vue dist, embedded here in a later phase.
