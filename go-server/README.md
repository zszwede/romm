# romm-go — single-binary RomM (Go rewrite)

A single static Go binary that reuses the EXISTING MariaDB schema (no data
migration) and the filesystem. No Redis, no nginx, no Python, no sidecar — the
only runtime dependencies are MariaDB and the ROM library on disk.

## Run
Uses the same env var names as the Python container:

    DB_HOST=192.168.1.201 DB_USER=romm-user DB_PASSWD=… DB_NAME=romm \
    ROMM_BASE_PATH=/media/8TB/Retro/.romm \
    ROMM_AUTH_SECRET_KEY=… IGDB_CLIENT_ID=… IGDB_CLIENT_SECRET=… \
    MERGE_SERVICE_URL=http://rommdownloader:8090 MERGE_SERVICE_TOKEN=… \
    ROMM_GO_ADDR=:8088 go run ./cmd/romm

## Endpoints
- `GET  /api/heartbeat` — version + live library counts
- `GET  /api/platforms` — platforms with rom counts
- `GET  /api/roms?platform_id=&limit=` · `GET /api/roms/{id}`
- `GET  /api/collections`
- `PUT  /api/roms/{id}/props` *(bearer)* — rom_user status/flags/rating
- `POST /api/scan` *(bearer)* · `GET /api/scan` · `GET /api/scan/{id}`
- `GET  /api/metadata/search?q=` *(bearer)*
- `POST /api/virtual/check` · `POST /api/virtual/dispatch` *(bearer)*
- `GET  /` — embedded static shell (Vue dist in a later step)

Bearer tokens are the Python backend's own HS256 access tokens (verified with
`ROMM_AUTH_SECRET_KEY`), so existing tokens work unchanged.

## Status
Core workflow implemented and tested (`go test -race ./...`, plus live checks
against pve2 + IGDB):

1. ✅ skeleton + DB + static serving + heartbeat/roms (read-only)
2. ✅ JWT auth + platforms/rom-detail reads + rom_user write
3. ✅ in-process worker queue (drops Redis) + filesystem scanner + native
   hashing + ES-DE gamelist.xml ingest.
4. ✅ metadata providers — IGDB (live-tested). ScreenScraper/Hasheous slot
   behind the same `Provider` interface when needed.
5. ✅ virtual downloads — **RommDownloader absorbed in-process** (internal/
   downloader + internal/downloadsvc): the binary runs the download→extract→
   switch-merge→deliver pipeline itself (external tools 7z/unar/switch-tool via
   exec). Set ROMM_DOWNLOADER_CONFIG to enable; else it dispatches to an
   external merge service. Applying a finished job's result back to the DB
   (mark physical + path) is the one remaining wire-up.
6. ✅ (scoped) collections read. **Intentionally out of the rewrite's initial
   scope** (ported later only if used): save/state sync, play-session
   streaming/netplay, OIDC/device-auth, music playlists, achievements,
   in-browser ROM patching.

The frontend stays the built Vue SPA, to be embedded into `web/static` (copied
from the existing image) in place of the current placeholder.

## Layout
`cmd/romm` (main) · `internal/config` · `internal/store` (database/sql) ·
`internal/auth` (JWT) · `internal/hash` · `internal/scan` · `internal/worker` ·
`internal/metadata` (IGDB) · `internal/virtual` (merge-service client) ·
`internal/httpapi` (net/http, `Deps` + `DataStore`/`VirtualService` interfaces
for test fakes) · `web` (embed.FS).
