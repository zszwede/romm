"""Virtual-ROM download orchestration.

A virtual game is a file-less row with one or more download URLs (base game +
updates + DLC in `Rom.virtual_contents`, plus mirror URLs). This module turns
that into work for an external *merge service* — a process that takes a list of
URLs, downloads/extracts/merges them into a single file, and calls RomM back —
and applies the callback that flips the game from virtual to physical.

Nothing here downloads anything itself; RomM only selects URLs (honouring the
user's preferred-host order), hands them to the merge service, and records the
result. The flow:

    D hotkey            -> mark_wanted(rom)            download_status = wanted
    dispatch (manual)   -> dispatch_rom(rom, order)    -> POST merge service,
                                                          download_status = queued
    merge service done  -> apply_callback(...)         is_virtual = False, file set,
                                                          download_status = done
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

import httpx2 as httpx

from config import VIRTUAL_DOWNLOAD_BASE_PATH
from config.config_manager import config_manager
from handler.database import (
    db_platform_handler,
    db_rom_handler,
    db_virtual_source_handler,
)
from logger.logger import log
from models.rom import DownloadStatus, Rom, RomFile, compute_full_path_hash

from .archive_org import host_of

def preferred_host_order(user: Any | None) -> list[str]:
    """The host preference order to use for `user`: their ui_settings
    `downloadHostOrder` if set, else the server default from config."""
    order = None
    settings = getattr(user, "ui_settings", None)
    if isinstance(settings, dict):
        raw = settings.get("downloadHostOrder")
        if isinstance(raw, list):
            order = [str(h).lower() for h in raw if h]
    if not order:
        order = [h.lower() for h in config_manager.config.DOWNLOADS.default_host_order]
    return order


def _rank(host: str | None, order: list[str]) -> int:
    """Rank of a host in the preference order; unlisted hosts sort last."""
    if host and host in order:
        return order.index(host)
    return len(order)


# Category sort order for the links handed to the merge service: the base
# game (incl. its parts) first, then updates, then DLC.
_CAT_RANK = {
    "base": 0, "base_game": 0, "game": 0, None: 0, "": 0,
    "update": 1, "patch": 1,
    "dlc": 2,
}


def select_download_items(
    rom: Rom, order: list[str], selected: set[str] | None = None
) -> list[dict]:
    """Every file to fetch for `rom`: all base parts + all updates + all DLC.

    A game can be multi-part (a base split into Part1/Part2 that don't exist
    separately) and can have several updates/DLC; the merge service needs the
    WHOLE set, so this returns one item per link — never collapsing parts to a
    single URL. Items are de-duplicated by URL and ordered base → update → DLC,
    with the preferred host first within each group and parts kept in order.

    When `selected` (a set of URLs) is given, only those links are returned —
    the user's explicit base/update/DLC choice from the Contents card.
    """
    contents = rom.virtual_contents or []
    if not contents and selected is None:
        # No captured set (shouldn't happen post-backfill): fall back to the
        # primary URL plus any mirrors as a single base file's alternatives.
        contents = [
            {"category": "base", "name": rom.fs_name, "url": u, "host": host_of(u)}
            for u in (rom.source_urls or ([rom.source_url] if rom.source_url else []))
        ]
    if selected is not None:
        contents = [c for c in contents if c.get("url") in selected]

    items: list[dict] = []
    seen: set[str] = set()
    for c in contents:
        url = c.get("url")
        if url and url not in seen:
            seen.add(url)
            items.append(dict(c))

    items.sort(
        key=lambda c: (
            _CAT_RANK.get(c.get("category"), 3),
            _rank(c.get("host") or host_of(c.get("url")), order),
            c.get("part") or 0,
        )
    )
    return items


def _is_base(item: dict) -> bool:
    """True for a base-game link (incl. its parts); false for update/DLC."""
    return _CAT_RANK.get(item.get("category"), 3) == 0


class BaseUnavailable(Exception):
    """A base-game link no longer exists upstream (confirmed by the availability
    check). Carries the dead base items and any surviving base alternatives so
    the caller can let the user pick a different base or cancel."""

    def __init__(self, dead: list[dict], alternatives: list[dict]):
        self.dead = dead
        self.alternatives = alternatives
        names = ", ".join(d.get("name") or d.get("url") or "?" for d in dead)
        super().__init__(f"base file not available: {names}")


async def check_availability(urls: list[str]) -> dict[str, dict]:
    """Ask the merge service which URLs still exist (``POST /api/v1/check``).

    Returns ``{url: {available, checked, reason, filename, size}}``. On any
    failure — no service configured, the endpoint missing (older service),
    network error — returns ``{}``; callers then treat every link as unchecked
    and skip pruning, so dispatch keeps working exactly as before.
    """
    cfg = config_manager.config.DOWNLOADS
    if not cfg.merge_service_url or not urls:
        return {}
    headers = {}
    if cfg.service_api_token:
        headers["Authorization"] = f"Bearer {cfg.service_api_token}"
    try:
        async with httpx.AsyncClient(timeout=120) as client:
            resp = await client.post(
                cfg.merge_service_url.rstrip("/") + "/api/v1/check",
                json={"urls": urls},
                headers=headers,
            )
        resp.raise_for_status()
        data = resp.json()
    except Exception as exc:
        log.warning("availability check failed (dispatching without it): %s", exc)
        return {}
    return {r["url"]: r for r in data.get("results", []) if r.get("url")}


def _prune_dead_links(rom: Rom, dead_urls: set[str]) -> None:
    """Remove confirmed-dead links from *everywhere* they are stored: the
    captured virtual_contents (JSON), the primary source_url, and the mirror
    rows. remove_source_url promotes a surviving mirror to primary (or clears
    it) so the game keeps a usable source when one exists."""
    if not dead_urls:
        return
    contents = rom.virtual_contents or []
    kept = [c for c in contents if c.get("url") not in dead_urls]
    if len(kept) != len(contents):
        db_rom_handler.update_rom(rom.id, {"virtual_contents": kept})
    for url in dead_urls:
        db_virtual_source_handler.remove_source_url(rom.id, url)
    log.info("Pruned %d dead link(s) from rom %s", len(dead_urls), rom.id)


def _set_status(rom_id: int, status: DownloadStatus | None) -> None:
    db_rom_handler.update_rom(rom_id, {"download_status": status and str(status)})


def mark_wanted(rom_id: int) -> None:
    """Flag a virtual game as wanted (the `D` hotkey). Idempotent."""
    _set_status(rom_id, DownloadStatus.WANTED)


def clear_status(rom_id: int) -> None:
    _set_status(rom_id, None)


async def dispatch_rom(
    rom: Rom, order: list[str], selected: set[str] | None = None
) -> str:
    """Hand a virtual game's URL set to the merge service and mark it queued.

    Speaks the RommDownloader API: ``POST {merge_service_url}/api/v1/jobs`` with
    ``Authorization: Bearer <service_api_token>`` and a job request carrying the
    system, the links, and `rom_id`/`platform_id` in `metadata` (echoed back on
    the callback). The merge service holds its own host credentials and its own
    callback target, so neither is sent here.

    Raises ValueError when no merge service is configured or the game has no
    downloadable URL.
    """
    cfg = config_manager.config.DOWNLOADS
    if not cfg.merge_service_url:
        raise ValueError("no merge service configured (downloads.merge_service_url)")

    items = select_download_items(rom, order, selected)
    if not items:
        raise ValueError("game has no downloadable URL")

    # Availability pre-check (at dispatch): confirm the files still exist before
    # queueing. Only links the service could actually check (checked=true) and
    # reported gone are treated as dead; unknown/unchecked links are left alone.
    avail = await check_availability([it["url"] for it in items])
    dead_urls = {
        it["url"]
        for it in items
        if (a := avail.get(it["url"])) and a.get("checked") and not a.get("available")
    }
    if dead_urls:
        dead_base = [it for it in items if it["url"] in dead_urls and _is_base(it)]
        # Surviving base candidates across the whole known set (virtual_contents
        # + source_url + mirrors), not just this selection — computed BEFORE the
        # prune so the user can pick a different base or give up.
        alternatives = [
            it
            for it in select_download_items(rom, order, None)
            if it["url"] not in dead_urls and _is_base(it)
        ]
        _prune_dead_links(rom, dead_urls)  # bad links removed from the DB
        if dead_base:
            raise BaseUnavailable(dead_base, alternatives)
        # Only updates/DLC were dead: drop them and carry on with the base.
        items = [it for it in items if it["url"] not in dead_urls]
        log.info("Dropped %d dead update/DLC link(s) for rom %s", len(dead_urls), rom.id)
    if not items:
        raise ValueError("game has no downloadable URL")

    platform = db_platform_handler.get_platform(rom.platform_id)
    system = platform.fs_slug if platform else ""

    payload = {
        "system": system,
        "name": rom.fs_name,
        "links": [
            {
                "url": it["url"],
                "name": it.get("name"),
                "category": it.get("category") or "base",
                "version": it.get("version"),
            }
            for it in items
        ],
        "metadata": {
            "rom_id": rom.id,
            "platform_id": rom.platform_id,
            "platform_slug": system,
        },
    }

    headers = {}
    if cfg.service_api_token:
        headers["Authorization"] = f"Bearer {cfg.service_api_token}"

    # A dedicated client, NOT the shared ctx_httpx_client: the merge service is
    # an admin-configured internal host (typically a private/container address),
    # which the shared client's SSRF guard — meant for user-supplied game URLs —
    # would reject. This target is operator-trusted, so the guard doesn't apply.
    async with httpx.AsyncClient(timeout=30) as client:
        resp = await client.post(
            cfg.merge_service_url.rstrip("/") + "/api/v1/jobs",
            json=payload,
            headers=headers,
        )
    resp.raise_for_status()
    _set_status(rom.id, DownloadStatus.QUEUED)
    log.info("Dispatched virtual rom %s (%d links) to merge service", rom.id, len(items))
    return "queued"


# The merge service reports an absolute path in its own container (e.g.
# /mnt/retroEMU/roms/switch/Game.nsp); every RomM library path contains the
# roms dir, so splitting there yields the library-relative path regardless of
# the service's mount prefix.
_ROMS_MARKER = "/roms/"


def _relativize(location: str) -> tuple[str, str]:
    """Split a reported file `location` into (library-relative dir, file name).

    Prefers the ``/roms/`` boundary (robust to any mount prefix the merge
    service uses); falls back to making an absolute path relative to the
    download base, else treats it as already library-relative.
    """
    norm = location.replace("\\", "/")
    idx = norm.find(_ROMS_MARKER)
    if idx >= 0:
        rel = Path(norm[idx + 1 :])  # drop the leading slash -> roms/<slug>/file
        return str(rel.parent), rel.name
    p = Path(location)
    if p.is_absolute():
        base = Path(VIRTUAL_DOWNLOAD_BASE_PATH).resolve()
        try:
            rel = p.resolve().relative_to(base)
        except ValueError:
            rel = Path(location.lstrip("/"))
    else:
        rel = Path(os.path.normpath(str(p)))
    return str(rel.parent), rel.name


def apply_callback(
    rom_id: int,
    platform_id: int | None,
    location: str,
    size: int | None = None,
    hashes: dict | None = None,
    status: str = "completed",
) -> Rom | None:
    """Apply a merge-service callback to a virtual game.

    On a non-completed `status` the game is marked FAILED and left virtual. On
    completion, `location` (where the merged file now lives, absolute or
    library-relative) is registered and the row is flipped to physical — the
    tail of `materialize_rom`. Returns the updated rom, or None if it is gone.
    """
    rom = db_rom_handler.get_rom(rom_id)
    if rom is None:
        return None
    if platform_id is not None and rom.platform_id != platform_id:
        raise ValueError(
            f"rom {rom_id} is on platform {rom.platform_id}, not {platform_id}"
        )
    if status != "completed":
        _set_status(rom_id, DownloadStatus.FAILED)
        return rom
    if not rom.is_virtual:
        # Already materialized (e.g. a duplicate callback): just record success.
        _set_status(rom_id, DownloadStatus.DONE)
        return db_rom_handler.get_rom(rom_id)
    if not location:
        raise ValueError("completed callback without a file location")

    file_path, file_name = _relativize(location)
    hashes = hashes or {}
    hash_fields = {
        k: hashes[k]
        for k in ("crc_hash", "md5_hash", "sha1_hash")
        if hashes.get(k)
    }

    db_rom_handler.add_rom_file(
        RomFile(
            rom_id=rom_id,
            file_name=file_name,
            file_path=file_path,
            file_size_bytes=size or 0,
            **hash_fields,
        )
    )
    # Keep fs_name as the logical (virtual) name like materialize_rom does — the
    # merged file's real basename lives on the RomFile, which is what resolves
    # the file on disk. Changing fs_name under a bulk update wouldn't recompute
    # its derived columns (fs_name_no_tags/_no_ext/fs_extension).
    db_rom_handler.update_rom(
        rom_id,
        {
            "is_virtual": False,
            "download_status": str(DownloadStatus.DONE),
            "fs_path": file_path,
            "full_path_hash": compute_full_path_hash(file_path, rom.fs_name),
            "fs_size_bytes": size or rom.fs_size_bytes,
            "missing_from_fs": False,
            **hash_fields,
        },
    )
    log.info("Virtual rom %s materialized via callback at %s", rom_id, location)
    return db_rom_handler.get_rom(rom_id)
