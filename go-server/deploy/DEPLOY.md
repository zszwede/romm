# Deploying the Go RomM server

The Go server is a single static binary. It reuses the existing MariaDB (pve2)
and the ROM library on disk — no Redis, nginx, Python, or sidecar.

## 1. Build
    cd /home/xiam/romm-fork/go-server && go build -o bin/romm-go ./cmd/romm

## 2. Frontend assets on disk
The ~400 MB in-browser player assets are served from disk (too big to embed).
Copy the built frontend out of the current image once:

    podman cp romm:/var/www/html /home/xiam/romm/go-webroot

## 3. Run (parallel, port 8099 — does NOT touch the container on :80)
Put the secrets in `/home/xiam/romm/go-server.env` (DB_PASSWD,
ROMM_AUTH_SECRET_KEY, IGDB_CLIENT_ID/SECRET), then use `deploy/romm-go.service`:

    cp deploy/romm-go.service ~/.config/systemd/user/
    systemctl --user daemon-reload && systemctl --user enable --now romm-go

Verify: `curl localhost:8099/api/heartbeat` and open the UI through a parallel
Caddy vhost (see `Caddyfile.snippet`, option 1).

## 4. Cutover (your decision)
The rewrite is a drop-in for the whole app — browse, search, play (EmulatorJS),
favorites, collections, saves/states sync + edits, scan, ROM upload, rom_user
state, notes, media, settings, user management, permissions, client tokens,
achievements, music, metadata, device sync, and the full virtual-download
feature (queue, merge, exclude, sources CRUD, the archive.org scraper). Verified
end to end in a browser with a zero-error console; 18 Go packages pass
`go test -race`, 6 integration tests against the live schema.

Metadata matching runs against **ScreenScraper** (this install's primary source,
platform-scoped) and **IGDB**; set `SCREENSCRAPER_DEV_ID`/`_DEV_PASSWORD` (+
`SCREENSCRAPER_USER`/`_PASSWORD` for the higher quota) and the IGDB pair in the
env file. The manual-match dialog (`GET /api/search/roms`) queries both and
merges hits per title. **Hasheous** (hash identification — a scan identifies
still-unmatched, hashed roms by their md5/sha1/crc) and **Libretro** (box-art
covers added to match results) are public keyless sources, on by default; set
`HASHEOUS_API_ENABLED=false` / `LIBRETRO_API_ENABLED=false` to turn either off.

The **one** feature it does not carry is remote-session **streaming** (the
session-host that streams real emulator processes over WebRTC — reported
disabled; play is in-browser via EmulatorJS instead).

**IMPORTANT:** the service must have `ROMM_LIBRARY_ROMS_DIR=/media/8TB/Retro/roms`
set (see `romm-go.service`) — the ES-DE library is an overlay the container sees
but the host does not, so without it ROM download/play (content serving) 404s.

**Also set `ROMM_CONFIG_FILE`** to RomM's `config.yml` (the host path, e.g.
`/home/xiam/romm/config/config.yml` — same overlay caveat: it defaults to
`ROMM_BASE_PATH/config/config.yml`, which is the container's view, not the
host's). It feeds the Settings UI — exclusion lists, scan priorities, EmulatorJS
and converto settings, platform bindings. Without it the UI shows "config file
not mounted" and those settings are blank. It is read-only at runtime (the Go
server serves it; it does not write config.yml back yet).

To cut over: point `romm.ai-monster.local` at `retro.local:8099` (see
`Caddyfile.snippet`, option 2) and reload Caddy. Roll back instantly by
restoring the previous `reverse_proxy` target and reloading — the container is
untouched and still running.
