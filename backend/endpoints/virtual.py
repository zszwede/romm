"""Virtual ROMs: assign archive.org sources to platforms, sync them, download one.

Sync and download run as background tasks on the event loop (a source can list
thousands of games; a download can be gigabytes), each wrapped in its own
``initialize_context`` inside the handler module.
"""

from __future__ import annotations

import asyncio
from datetime import datetime
from typing import Annotated, Any, Coroutine

from fastapi import Body, HTTPException, Request, status
from pydantic import BaseModel, ConfigDict, Field

from decorators.auth import protected_route
from handler.auth.constants import Scope
from handler.database import (
    db_platform_handler,
    db_rom_handler,
    db_virtual_source_handler,
)
from handler.filesystem import fs_virtual_handler
from handler.scan_handler import scan_platform
from handler.virtual.archive_org import ArchiveOrgError, parse_source_url
from handler.virtual.sync import materialize_rom, sync_platform_sources
from logger.logger import log
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


@protected_route(router.post, "/roms/{rom_id}/download", [Scope.ROMS_WRITE])
async def download_virtual_rom(request: Request, rom_id: int) -> dict[str, str]:
    """Download a virtual ROM's file from its source; it becomes a normal ROM."""
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
    _launch(materialize_rom(rom_id))
    return {"msg": "Download started"}
