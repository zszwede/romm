"""Create virtual ROMs from archive.org sources, and materialize them on demand.

Both run as background tasks (a source can list thousands of games, and a
download can be gigabytes), each under its own ``initialize_context`` so the
shared httpx client is available off the request cycle.
"""

from __future__ import annotations

import hashlib
import os
import zlib

from sqlalchemy.exc import IntegrityError

from handler.database import (
    db_platform_handler,
    db_rom_handler,
    db_virtual_source_handler,
)
from handler.filesystem import fs_rom_handler, fs_virtual_handler
from handler.filesystem.base_handler import (
    REGION_NAME_TO_PROVIDER_SHORTCODE,
    normalize_region,
)
from handler.scan_handler import (
    MetadataSource,
    ScanType,
    build_hashless_fs_rom,
    build_virtual_fs_path,
    download_rom_resources,
    scan_rom,
)
from logger.logger import log
from models.base import compute_file_name_no_tags
from models.platform import Platform
from models.rom import Rom, RomFile, compute_full_path_hash
from models.virtual_rom_source import VirtualRomSource
from utils.context import ctx_httpx_client, initialize_context
from utils.filesystem import sanitize_filename

from .archive_org import VirtualGame, list_games

# Region spellings the allow-list accepts beyond what `normalize_region` knows
# (canonical names + single-letter filename shortcodes like E/U/J). Covers the
# provider two-letter codes (eu, us, jp, ...) and common casual forms, so a user
# can type EU, EUR, Europe, E; US, USA, U; JP, JPN, Japan; and so on.
_PROVIDER_CODE_TO_REGION = {
    code.lower(): name for name, code in REGION_NAME_TO_PROVIDER_SHORTCODE.items()
}
_EXTRA_REGION_ALIASES = {
    "eur": "Europe",
    "jpn": "Japan",
    "ger": "Germany",
    "fra": "France",
    "spa": "Spain",
    "ita": "Italy",
    "aus": "Australia",
    "america": "USA",
    "americas": "Americas",
}


def _resolve_region(value: str) -> str | None:
    key = value.strip().lower()
    return (
        normalize_region(value)
        or _PROVIDER_CODE_TO_REGION.get(key)
        or _EXTRA_REGION_ALIASES.get(key)
    )

# A download is read and hashed in chunks of this size.
_DOWNLOAD_CHUNK = 1024 * 1024


def _normalized_allow(region_filter: list | None) -> set[str]:
    allow: set[str] = set()
    for raw in region_filter or []:
        canonical = _resolve_region(str(raw))
        if canonical:
            allow.add(canonical)
    return allow


def _region_ok(regions: list[str], allow: set[str]) -> bool:
    # With no filter, keep everything. With a filter, keep a game when it carries
    # an allowed region OR has no region tag at all (an untagged ROM is accepted,
    # never silently dropped); only drop games whose region is known and excluded.
    return not allow or not regions or bool(set(regions) & allow)


async def _create_virtual_rom(
    platform: Platform, game: VirtualGame, allow: set[str], metadata_sources: list[str]
) -> str:
    """Create one virtual rom. Returns 'created', 'skipped' or 'failed'."""
    fs_name = sanitize_filename(game.name)
    parsed = fs_rom_handler.parse_tags(fs_name)
    if not _region_ok(parsed.regions, allow):
        return "skipped"

    fs_path = build_virtual_fs_path(platform)
    try:
        rom = db_rom_handler.add_rom(
            Rom(
                platform_id=platform.id,
                fs_name=fs_name,
                fs_path=fs_path,
                fs_size_bytes=game.size or 0,
                name=compute_file_name_no_tags(fs_name),
                is_virtual=True,
                source_url=game.source_url,
                regions=parsed.regions,
                languages=parsed.languages,
                revision=parsed.revision,
                version=parsed.version,
                tags=parsed.other_tags,
                url_cover="",
                url_manual="",
                url_screenshots=[],
            )
        )
    except IntegrityError:
        # The unique (platform_id, full_path_hash) index already has this title.
        return "skipped"

    # Fetch metadata/media like a physical game, but keep the basic virtual row
    # if that fails: it is still downloadable, just without a cover yet.
    try:
        scanned = await scan_rom(
            scan_type=ScanType.QUICK,
            platform=platform,
            rom=rom,
            fs_rom=build_hashless_fs_rom(fs_name, fs_path, flat=True),
            metadata_sources=metadata_sources,
            newly_added=True,
        )
        added = db_rom_handler.add_rom(scanned)
        await download_rom_resources(
            added_rom=added,
            previous_url_cover=rom.url_cover,
            previous_url_manual=rom.url_manual,
            previous_url_screenshots=rom.url_screenshots,
            metadata_sources=metadata_sources,
        )
    except Exception:
        log.exception("Virtual rom %s created but metadata lookup failed", fs_name)
    return "created"


async def sync_source(source: VirtualRomSource) -> str:
    """Fetch a source and create a virtual rom per listed game it is missing."""
    platform = db_platform_handler.get_platform(source.platform_id)
    if platform is None:
        return "platform gone"

    games, kind = await list_games(source.url, rom_extensions=frozenset())
    if source.kind != kind:
        db_virtual_source_handler.update_source(source.id, {"kind": kind})

    allow = _normalized_allow(source.region_filter)
    metadata_sources = [s.value for s in MetadataSource]
    counts = {"created": 0, "skipped": 0, "failed": 0}
    for game in games:
        try:
            counts[await _create_virtual_rom(platform, game, allow, metadata_sources)] += 1
        except Exception:
            log.exception("Failed to add virtual rom %s", game.name)
            counts["failed"] += 1

    if counts["created"]:
        db_rom_handler.invalidate_filter_values_cache()
    result = f"{counts['created']} added, {counts['skipped']} already present"
    if counts["failed"]:
        result += f", {counts['failed']} failed"
    return result


async def sync_platform_sources(platform_id: int) -> None:
    """Background entry point: sync every enabled source of a platform."""
    async with initialize_context():
        sources = db_virtual_source_handler.get_sources(
            platform_id=platform_id, enabled_only=True
        )
        for source in sources:
            try:
                result = await sync_source(source)
            except Exception as exc:  # noqa: BLE001 - record and continue
                log.exception("Virtual source %s sync failed", source.url)
                result = f"error: {exc}"
            db_virtual_source_handler.mark_synced(source.id, result)
            log.info("Virtual source %s: %s", source.url, result)


async def materialize_rom(rom_id: int) -> None:
    """Background entry point: download a virtual rom's file and make it normal."""
    async with initialize_context():
        rom = db_rom_handler.get_rom(rom_id)
        if rom is None or not rom.is_virtual or not rom.source_url:
            return
        platform = db_platform_handler.get_platform(rom.platform_id)
        if platform is None:
            return

        dest = fs_virtual_handler.rom_write_path(platform.fs_slug, rom.fs_name)
        await fs_virtual_handler.ensure_platform_dirs(platform.fs_slug)

        tmp = f"{dest}.part"
        sha1 = hashlib.sha1()  # noqa: S324 - matches RomM's existing rom hashing
        md5 = hashlib.md5()  # noqa: S324
        crc = 0
        size = 0
        client = ctx_httpx_client.get()
        try:
            async with client.stream(
                "GET", rom.source_url, timeout=None, follow_redirects=True
            ) as response:
                response.raise_for_status()
                with open(tmp, "wb") as fh:
                    async for chunk in response.aiter_bytes(_DOWNLOAD_CHUNK):
                        fh.write(chunk)
                        sha1.update(chunk)
                        md5.update(chunk)
                        crc = zlib.crc32(chunk, crc)
                        size += len(chunk)
            os.replace(tmp, dest)
        except Exception:
            log.exception("Virtual rom %s download failed", rom.fs_name)
            if os.path.exists(tmp):
                os.remove(tmp)
            return

        new_fs_path = fs_virtual_handler.roms_dir(platform.fs_slug)
        hashes = {
            "crc_hash": format(crc & 0xFFFFFFFF, "08x"),
            "md5_hash": md5.hexdigest(),
            "sha1_hash": sha1.hexdigest(),
        }
        # Serve the real file: a normal rom needs a RomFile the download endpoint
        # streams. file_path is relative to the library, so it resolves the same
        # whether read through the library mount or written through the base.
        db_rom_handler.add_rom_file(
            RomFile(
                rom_id=rom_id,
                file_name=rom.fs_name,
                file_path=new_fs_path,
                file_size_bytes=size,
                **hashes,
            )
        )
        db_rom_handler.update_rom(
            rom_id,
            {
                "is_virtual": False,
                "fs_path": new_fs_path,
                "full_path_hash": compute_full_path_hash(new_fs_path, rom.fs_name),
                "fs_size_bytes": size,
                "missing_from_fs": False,
                **hashes,
            },
        )
        log.info("Materialized virtual rom %s (%d bytes)", rom.fs_name, size)


__all__ = ["sync_platform_sources", "sync_source", "materialize_rom"]
