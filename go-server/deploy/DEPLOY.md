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
The rewrite implements the core workflow (browse, scan, metadata, rom_user
state, virtual downloads). It **intentionally omits**, and therefore cutover
**loses until ported**: save/state sync, play-session streaming/netplay,
OIDC/device auth, music playlists, achievements, in-browser ROM patching.

When you accept that trade-off, point `romm.ai-monster.local` at
`retro.local:8099` (see `Caddyfile.snippet`, option 2) and reload Caddy. Roll
back by restoring the previous `reverse_proxy` target — the container is
untouched and still running.
