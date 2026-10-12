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
