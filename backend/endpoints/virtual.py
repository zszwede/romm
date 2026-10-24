"""Virtual ROMs: assign archive.org sources to platforms, sync them, download one.

Sync and download run as background tasks on the event loop (a source can list
thousands of games; a download can be gigabytes), each wrapped in its own
``initialize_context`` inside the handler module.
"""

from __future__ import annotations

import asyncio
from datetime import datetime
from typing import Annotated, Any, Coroutine

from fastapi import Body, Header, HTTPException, Request, status
from pydantic import BaseModel, ConfigDict, Field

from config.config_manager import config_manager
from decorators.auth import protected_route
from handler.auth.constants import Scope
from handler.database import (
    db_platform_handler,
    db_rom_handler,
    db_virtual_source_handler,
)
from handler.filesystem import fs_virtual_handler
from handler.scan_handler import MetadataSource, scan_platform
from handler.virtual import download, resolver
from handler.virtual.archive_org import (
    ArchiveOrgError,
    VirtualGame,
    host_of,
    parse_source_url,
)
from handler.virtual.sync import (
    _create_virtual_rom,
    materialize_rom,
    sync_platform_sources,
)
from logger.logger import log
from models.base import compute_file_name_no_ext
from models.rom import DownloadStatus
from models.virtual_rom_source import VirtualRomSource
from utils.router import APIRouter

router = APIRouter(prefix="/virtual", tags=["virtual"])

# Hold references so background tasks are not garbage-collected mid-run.
_BACKGROUND_TASKS: set[asyncio.Task] = set()


def _launch(coro: Coroutine[Any, Any, Any]) -> None:
    task = asyncio.create_task(coro)
    _BACKGROUND_TASKS.add(task)
    task.add_done_callback(_BACKGROUND_TASKS.discard)


class VirtualSourceSchema(BaseModel):
    id: int
    platform_id: int
    url: str
    kind: str | None
    region_filter: list | None
    enabled: bool
    last_synced_at: datetime | None
    last_result: str | None

    model_config = ConfigDict(from_attributes=True)


class AssignForm(BaseModel):
    urls: list[str] = Field(..., min_length=1, description="archive.org source URLs.")
    platform_id: int | None = Field(
        default=None, description="Existing platform to assign to."
    )
    fs_slug: str | None = Field(
        default=None, description="Slug of a platform to create when none is given."
    )
    region_filter: list[str] | None = Field(
        default=None, description="Keep only these regions, e.g. ['USA','Europe']."
    )
    sync: bool = Field(default=True, description="Start a sync right after assigning.")


def _validate_urls(urls: list[str]) -> None:
    for url in urls:
        try:
            parse_source_url(url)
        except ArchiveOrgError as exc:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
                detail=f"Not a recognised archive.org URL: {url}",
            ) from exc


@protected_route(router.post, "/assign", [Scope.PLATFORMS_WRITE])
async def assign_sources(
    request: Request,
    form_data: Annotated[AssignForm, Body()],
) -> list[VirtualSourceSchema]:
    """Assign archive.org links to a platform, creating it (and its folders) if new."""
    _validate_urls(form_data.urls)

    platform = None
    if form_data.platform_id is not None:
        platform = db_platform_handler.get_platform(form_data.platform_id)
    elif form_data.fs_slug:
        platform = db_platform_handler.get_platform_by_fs_slug(form_data.fs_slug)

    if platform is None:
        fs_slug = (form_data.fs_slug or "").strip()
        if not fs_slug:
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
                detail="A platform_id or a fs_slug for a new platform is required",
            )
        # Create the on-disk structure on the writable base first, then the row.
        await fs_virtual_handler.ensure_platform_dirs(fs_slug)
        scanned = await scan_platform(fs_slug, [fs_slug])
        platform = db_platform_handler.add_platform(scanned)
    else:
        # Make sure an existing platform has the media folders too.
        await fs_virtual_handler.ensure_platform_dirs(platform.fs_slug)

    created = [
        db_virtual_source_handler.add_source(
            VirtualRomSource(
                platform_id=platform.id,
                url=url.strip(),
                region_filter=form_data.region_filter or None,
            )
        )
        for url in form_data.urls
    ]

    if form_data.sync:
        _launch(sync_platform_sources(platform.id))

    log.info(
        "Assigned %d archive.org source(s) to platform %s",
        len(created),
        platform.fs_slug,
    )
    return [VirtualSourceSchema.model_validate(s) for s in created]


@protected_route(router.get, "/sources", [Scope.PLATFORMS_READ])
def list_sources(
    request: Request,
    platform_id: int | None = None,
) -> list[VirtualSourceSchema]:
    """List archive.org sources, optionally for one platform."""
    sources = db_virtual_source_handler.get_sources(platform_id=platform_id)
    return [VirtualSourceSchema.model_validate(s) for s in sources]


class UpdateSourceForm(BaseModel):
    """Patch an existing source. Only the provided fields change."""

    url: str | None = Field(default=None, description="New archive.org source URL.")
    region_filter: list[str] | None = Field(
        default=None, description="Replace the region filter (empty list clears it)."
    )
    enabled: bool | None = Field(
        default=None, description="Enable/disable without deleting."
    )


@protected_route(router.patch, "/sources/{id}", [Scope.PLATFORMS_WRITE])
def update_source(
    request: Request,
    id: int,
    form_data: Annotated[UpdateSourceForm, Body()],
) -> VirtualSourceSchema:
    """Edit a source in place (URL, region filter, enabled)."""
    source = db_virtual_source_handler.get_source(id)
    if source is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Source not found"
        )

    data = form_data.model_dump(exclude_unset=True)
    if "url" in data:
        if data["url"] is None or not data["url"].strip():
            raise HTTPException(
                status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
                detail="URL cannot be empty",
            )
        data["url"] = data["url"].strip()
        _validate_urls([data["url"]])
    if "region_filter" in data and not data["region_filter"]:
        # An empty/None list clears the filter (keep everything).
        data["region_filter"] = None

    db_virtual_source_handler.update_source(id, data)
    updated = db_virtual_source_handler.get_source(id)
    log.info("Updated virtual source %d: %s", id, ", ".join(sorted(data)))
    return VirtualSourceSchema.model_validate(updated)


@protected_route(router.delete, "/sources/{id}", [Scope.PLATFORMS_WRITE])
def delete_source(request: Request, id: int) -> dict[str, str]:
    """Remove a source. Virtual ROMs already created from it are left in place."""
    db_virtual_source_handler.delete_source(id)
    return {"msg": "Source deleted"}


@protected_route(router.post, "/platforms/{platform_id}/sync", [Scope.PLATFORMS_WRITE])
async def sync_platform(request: Request, platform_id: int) -> dict[str, str]:
    """Re-read every enabled source of a platform and add any new virtual ROMs."""
    platform = db_platform_handler.get_platform(platform_id)
    if platform is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Platform not found"
        )
    _launch(sync_platform_sources(platform_id))
    return {"msg": "Sync started"}


class MergeForm(BaseModel):
    rom_ids: list[int] = Field(
        ...,
        min_length=2,
        max_length=50,
        description="The ROMs to join into one. All but the keeper are folded in.",
    )


class RemoveSourceForm(BaseModel):
    url: str = Field(..., description="The download source URL to remove.")


@protected_route(router.post, "/merge", [Scope.ROMS_WRITE])
def merge_roms(
    request: Request,
    form_data: Annotated[MergeForm, Body()],
) -> dict[str, Any]:
    """Join two or more entries of the same game into one, keeping the local copy.

    Picks a single keeper and folds every other selected ROM into it: each
    other's archive.org download URLs become mirrors on the keeper, then it is
    deleted. A physical (local-file) ROM always wins as the keeper so the local
    copy keeps precedence; every absorbed ROM must be virtual, so nothing on
    disk is lost. Among virtual-only selections the identified ROM is kept, else
    the lowest id, so the keeper is deterministic.
    """
    # De-dupe while keeping order, so a doubled id can't become its own absorbed.
    ids = list(dict.fromkeys(form_data.rom_ids))
    if len(ids) < 2:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_CONTENT,
            detail="Pick at least two different ROMs to merge",
        )
    roms_by_id = {r.id: r for r in db_rom_handler.get_roms_by_ids(ids)}
    roms = [roms_by_id[i] for i in ids if i in roms_by_id]
    if len(roms) != len(ids):
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="ROM not found"
        )
    if len({r.platform_id for r in roms}) > 1:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail="All ROMs must be on the same platform",
        )

    # A physical row can't be absorbed without losing its local file, so at
    # most one may take part and it must be the keeper. Deterministic keeper:
    # physical first, then identified, then lowest id. Everything else is
    # virtual and gets folded in.
    if sum(not r.is_virtual for r in roms) > 1:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail="Can't merge more than one local ROM; the rest must be virtual",
        )
    keeper = sorted(roms, key=lambda r: (r.is_virtual, not r.is_identified, r.id))[0]
    absorbed = [r for r in roms if r.id != keeper.id]

    absorbed_ids: list[int] = []
    for rom in absorbed:
        merged = db_virtual_source_handler.merge_roms(keeper.id, rom.id)
        if merged is None:
            raise HTTPException(
                status_code=status.HTTP_409_CONFLICT,
                detail=f"Merge failed for ROM {rom.id}",
            )
        absorbed_ids.append(rom.id)
    db_rom_handler.invalidate_filter_values_cache()
    log.info("Merged virtual roms %s into rom %s", absorbed_ids, keeper.id)
    return {
        "msg": "ROMs merged",
        "keeper_id": keeper.id,
        "absorbed_ids": absorbed_ids,
    }


@protected_route(router.delete, "/roms/{rom_id}/source", [Scope.ROMS_WRITE])
def remove_rom_source(
    request: Request,
    rom_id: int,
    form_data: Annotated[RemoveSourceForm, Body()],
) -> dict[str, str]:
    """Remove one download source (mirror) from a ROM."""
    rom = db_rom_handler.get_rom(rom_id)
    if rom is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="ROM not found"
        )
    changed = db_virtual_source_handler.remove_source_url(rom_id, form_data.url)
    if not changed:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="ROM has no such source URL",
        )
    return {"msg": "Source removed"}


class DownloadForm(BaseModel):
    direct_url: str | None = Field(
        default=None,
        description="A final file URL captured in a browser for a captcha source.",
    )
    resolve_url: str | None = Field(
        default=None,
        description="A source URL to resolve server-side (scrape/form) and fetch.",
    )


# File-host flows the server can resolve without a human.
_AUTO_FLOWS = frozenset({"mediafire", "vikingfile", "filekeeper"})


@protected_route(router.post, "/roms/{rom_id}/download", [Scope.ROMS_WRITE])
async def download_virtual_rom(
    request: Request,
    rom_id: int,
    form_data: Annotated[DownloadForm, Body()] = DownloadForm(),
) -> dict[str, Any]:
    """Download a virtual ROM's file from its source; it becomes a normal ROM.

    When every source sits behind a captcha/Cloudflare gate that no server
    request can pass, nothing is started and ``status: "browser_required"`` is
    returned with the page URLs to open — the client shows a dialog, the user
    solves the gate and re-calls with ``direct_url`` set to the final file URL.
    """
    rom = db_rom_handler.get_rom(rom_id)
    if rom is None:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="ROM not found"
        )
    if not rom.is_virtual or not rom.source_url:
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail="ROM is not virtual or has no source URL",
        )

    # A user-supplied final URL bypasses classification: fetch it directly.
    if form_data.direct_url:
        _launch(materialize_rom(rom_id, direct_url=form_data.direct_url))
        return {"msg": "Download started", "status": "started"}
    # Resolve one specific source server-side (the dialog's "fetch automatically").
    if form_data.resolve_url:
        _launch(materialize_rom(rom_id, resolve_url=form_data.resolve_url))
        return {"msg": "Download started", "status": "started"}

    # Classify sources (no network). A direct archive.org/github/.pkg link is
    # fetched straight away (archive.org via the configured account). Otherwise
    # the game's sources are non-archive file-host pages: hand them to the client
    # so the capture dialog can DISPLAY the page and the user can solve its gate
    # (or one-click auto-resolve the hosts we can scrape). This is what the
    # "non-archive page -> dialog" requirement asks for.
    kinds = {}
    for url in rom.source_urls:
        try:
            kinds[url] = resolver.source_kind(url)
        except resolver.ResolveError:
            continue
    if any(k == "direct" for k in kinds.values()):
        _launch(materialize_rom(rom_id))
        return {"msg": "Download started", "status": "started"}

    pages = [
        {"url": url, "auto": kind in _AUTO_FLOWS}
        for url, kind in kinds.items()
    ]
    if pages:
        return {
            "msg": "Browser verification required",
            "status": "browser_required",
            "pages": pages,
        }

    # No usable source at all.
    raise HTTPException(
        status_code=status.HTTP_409_CONFLICT,
        detail="No usable download source for this ROM",
    )


# ---------------------------------------------------------------------------
# Download orchestration: flag (D), dispatch to the merge service, the queue an
# external merge service polls, and the callback it posts back when done.
# ---------------------------------------------------------------------------


class ManualLink(BaseModel):
    url: str = Field(..., min_length=1, description="A host download URL.")
    # base | update | dlc — controls merge/order; non-Switch platforms just use base.
    category: str = "base"
    name: str | None = None
    version: str | None = None


class ManualGameForm(BaseModel):
    platform_id: int
    name: str = Field(..., min_length=1)
    links: list[ManualLink] = Field(..., min_length=1)


@protected_route(router.post, "/roms", [Scope.ROMS_WRITE])
async def add_manual_rom(
    request: Request, form_data: Annotated[ManualGameForm, Body()]
) -> dict[str, Any]:
    """Manually create a virtual (downloadable) ROM on a platform from user-
    supplied host URLs.

    Reuses the sync's creation path (filename/tag parsing, dedup, and
    mirror-linking onto an existing row) with region filtering disabled
    (allow=set()). The platform decides the download pipeline at dispatch time
    (its fs_slug resolves to a downloader system; non-configured platforms use
    the generic default_system). Metadata/cover is fetched best-effort.
    """
    platform = db_platform_handler.get_platform(form_data.platform_id)
    if platform is None:
        raise HTTPException(status.HTTP_404_NOT_FOUND, detail="Platform not found")

    contents = tuple(
        {
            "category": (link.category or "base").lower(),
            "name": link.name or form_data.name,
            "url": link.url,
            "host": host_of(link.url),
            "version": link.version,
        }
        for link in form_data.links
    )
    base_url = next(
        (l.url for l in form_data.links if (l.category or "base").lower() == "base"),
        form_data.links[0].url,
    )
    game = VirtualGame(
        name=form_data.name, source_url=base_url, contents=contents
    )
    outcome, rom = await _create_virtual_rom(
        platform,
        game,
        allow=set(),  # no region filtering for a manual add
        metadata_sources=[s.value for s in MetadataSource],
        source_id=None,
        excluded=None,
    )
    if rom is None:
        raise HTTPException(
            status.HTTP_409_CONFLICT,
            detail=f"Could not add game (outcome: {outcome})",
        )
    return {
        "msg": f"Game {outcome}",
        "outcome": outcome,  # created | linked
        "rom_id": rom.id,
        "platform_id": platform.id,
    }


def _base_unavailable_detail(exc: "download.BaseUnavailable") -> dict[str, Any]:
    """Structured 409 body for a dead base file: the error code the frontend
    keys on, a human message, the dead base link(s), and any surviving base
    alternatives the user can switch to."""

    def _slim(it: dict) -> dict:
        return {
            "name": it.get("name"),
            "url": it.get("url"),
            "host": it.get("host"),
            "version": it.get("version"),
        }

    return {
        "error": "base_unavailable",
        "message": str(exc),
        "dead": [_slim(d) for d in exc.dead],
        "alternatives": [_slim(a) for a in exc.alternatives],
    }


def _require_virtual(rom_id: int):
    rom = db_rom_handler.get_rom(rom_id)
    if rom is None:
        raise HTTPException(status.HTTP_404_NOT_FOUND, detail="ROM not found")
    if not rom.is_virtual:
        raise HTTPException(status.HTTP_409_CONFLICT, detail="ROM is not virtual")
    return rom


@protected_route(router.post, "/roms/{rom_id}/mark-download", [Scope.ROMS_WRITE])
async def mark_download(request: Request, rom_id: int) -> dict[str, Any]:
    """The `D` hotkey. Flags a virtual game as wanted; when a merge service is
    configured it also dispatches it immediately (→ queued), so the loop is
    closed with one keypress. Without a service it stays `wanted` for a later
    dispatch or an external poll of `/virtual/queued`."""
    rom = _require_virtual(rom_id)
    download.mark_wanted(rom_id)
    if config_manager.config.DOWNLOADS.merge_service_url:
        order = download.preferred_host_order(request.user)
        try:
            result = await download.dispatch_rom(rom, order)
            return {"msg": "Dispatched", "status": result}
        except download.BaseUnavailable as exc:
            # Base file is gone: keep the game flagged wanted and tell the UI so
            # the user can pick a different base or cancel.
            raise HTTPException(
                status.HTTP_409_CONFLICT, detail=_base_unavailable_detail(exc)
            ) from exc
        except ValueError as exc:
            # Nothing to download — a real problem, not a transient one.
            raise HTTPException(status.HTTP_409_CONFLICT, detail=str(exc)) from exc
        except Exception as exc:  # service down/unreachable: keep the flag
            log.warning("dispatch of rom %s failed, left wanted: %s", rom_id, exc)
            return {
                "msg": "Flagged (dispatch failed; will retry)",
                "status": str(DownloadStatus.WANTED),
                "dispatch_error": str(exc),
            }
    return {"msg": "Marked for download", "status": str(DownloadStatus.WANTED)}


@protected_route(router.delete, "/roms/{rom_id}/mark-download", [Scope.ROMS_WRITE])
def unmark_download(request: Request, rom_id: int) -> dict[str, str]:
    """Clear a virtual game's download flag/status."""
    _require_virtual(rom_id)
    download.clear_status(rom_id)
    return {"msg": "Download flag cleared"}


class DispatchForm(BaseModel):
    # Optional explicit selection of which links to fetch (the Contents card's
    # base/update/DLC choice). Empty/omitted = the whole captured set.
    urls: list[str] | None = None


@protected_route(router.post, "/roms/{rom_id}/dispatch", [Scope.ROMS_WRITE])
async def dispatch_download(
    request: Request,
    rom_id: int,
    form_data: Annotated[DispatchForm, Body()] = DispatchForm(),
) -> dict[str, Any]:
    """Resolve a virtual game's URL set (or the user's selected subset) and hand
    it to the merge service now, marking it queued. 409 if no merge service is
    configured."""
    rom = _require_virtual(rom_id)
    order = download.preferred_host_order(request.user)
    selected = set(form_data.urls) if form_data.urls else None
    try:
        result = await download.dispatch_rom(rom, order, selected)
    except download.BaseUnavailable as exc:
        raise HTTPException(
            status.HTTP_409_CONFLICT, detail=_base_unavailable_detail(exc)
        ) from exc
    except ValueError as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, detail=str(exc)) from exc
    return {"msg": "Dispatched", "status": result}


def _check_callback_token(token: str | None) -> None:
    """Shared-secret gate for the external merge service (the callback + queue).
    Rejects when no secret is configured so these endpoints are never open."""
    configured = config_manager.config.DOWNLOADS.callback_token
    if not configured or token != configured:
        raise HTTPException(
            status.HTTP_401_UNAUTHORIZED, detail="Invalid or missing callback token"
        )


@router.get("/queued")
def download_queue(
    request: Request,
    x_callback_token: Annotated[str | None, Header()] = None,
    platform_id: int | None = None,
) -> dict[str, Any]:
    """The virtual games waiting to be downloaded, with their resolved URL sets,
    for an external merge service to poll. Authenticated by the shared callback
    token. Returns items flagged `wanted` or already `queued`."""
    _check_callback_token(x_callback_token)
    roms = db_virtual_source_handler.download_queue(
        [str(DownloadStatus.WANTED), str(DownloadStatus.QUEUED)], platform_id
    )
    order = [h.lower() for h in config_manager.config.DOWNLOADS.default_host_order]
    return {
        "items": [
            {
                "rom_id": rom.id,
                "platform_id": rom.platform_id,
                "name": rom.fs_name,
                "status": rom.download_status,
                "urls": download.select_download_items(rom, order),
            }
            for rom in roms
        ]
    }


class CallbackForm(BaseModel):
    """The RommDownloader job callback payload. `rom_id`/`platform_id` ride in
    `metadata` (RomM put them there on dispatch and the service echoes them),
    and the delivered file is `path` (with `files[].size` for its size)."""

    model_config = ConfigDict(extra="ignore")

    event: str | None = None
    job_id: str | None = None
    status: str | None = None
    metadata: dict[str, Any] = Field(default_factory=dict)
    path: str | None = None
    paths: list[str] = Field(default_factory=list)
    files: list[dict[str, Any]] = Field(default_factory=list)
    error: str | None = None


@router.post("/callback")
def download_callback(
    request: Request,
    form_data: Annotated[CallbackForm, Body()],
    x_callback_token: Annotated[str | None, Header()] = None,
) -> dict[str, Any]:
    """Merge service → RomM: a job finished. On success the game is marked
    physical and pointed at the delivered file; on failure it is marked failed.
    Authenticated by the shared callback token (downloads.callback_token)."""
    _check_callback_token(x_callback_token)
    rom_id = form_data.metadata.get("rom_id")
    if rom_id is None:
        raise HTTPException(
            status.HTTP_400_BAD_REQUEST, detail="metadata.rom_id is required"
        )
    platform_id = form_data.metadata.get("platform_id")
    completed = (form_data.status or "").lower() == "completed"
    location = form_data.path or (form_data.paths[0] if form_data.paths else "")
    size = None
    if form_data.files:
        size = form_data.files[0].get("size")
    try:
        rom = download.apply_callback(
            int(rom_id),
            int(platform_id) if platform_id is not None else None,
            location,
            size=size,
            status="completed" if completed else "failed",
        )
    except (ValueError, TypeError) as exc:
        raise HTTPException(status.HTTP_409_CONFLICT, detail=str(exc)) from exc
    if rom is None:
        raise HTTPException(status.HTTP_404_NOT_FOUND, detail="ROM not found")
    return {"msg": "ok", "rom_id": rom.id, "status": rom.download_status}


@protected_route(router.post, "/roms/{rom_id}/exclude", [Scope.ROMS_WRITE])
def exclude_rom(request: Request, rom_id: int) -> dict[str, str]:
    """Delete a virtual game AND exclude it, so a future list re-import never
    recreates it (the `Del` hotkey). Only virtual games — a physical file is
    never deleted this way."""
    rom = _require_virtual(rom_id)
    name = compute_file_name_no_ext(rom.fs_name)
    db_virtual_source_handler.add_exclusion(rom.platform_id, name)
    db_rom_handler.delete_rom(rom_id)
    return {"msg": "ROM deleted and excluded"}
