"""Report a virtual (archive.org) sync through the normal scan socket events.

A virtual sync runs off the request cycle as an asyncio task, not as an RQ scan
job, so it has no job meta to drive the UI. Instead it emits the very same
socket events a real library scan does — ``scan:scanning_platform``,
``scan:scanning_rom``, ``scan:update_stats`` and ``scan:done`` — so the v2
``useScanLifecycle`` wiring lights up the existing ScanningIndicator, live log
and gallery refresh with no frontend change at all.

Payload shapes mirror ``endpoints/sockets/scan.py`` exactly (that module is an
endpoint and must not be imported from a handler, so the small ``scan_stats``
dict is rebuilt here to match ``ScanStats.to_dict()``).
"""

from __future__ import annotations

from typing import Any

import socketio

from endpoints.responses.platform import PlatformSchema
from handler.scan_handler import emit_scanning_rom
from handler.socket_handler import socket_handler
from models.platform import Platform
from models.rom import Rom


class VirtualSyncProgress:
    """Accumulates counts across a platform's sources and emits scan events.

    One instance per ``sync_platform_sources`` run. ``total`` grows as each
    source's game list is read; ``scanned``/``new``/``identified`` climb as
    games are processed. A sync that hits the socket layer problems should never
    take the sync down, so every emit is best-effort.
    """

    def __init__(self, platform: Platform) -> None:
        self._platform = platform
        self.total = 0
        self.scanned = 0
        self.new = 0
        self.identified = 0

    def _manager(self) -> socketio.AsyncRedisManager:
        return socket_handler.write_manager()

    def _stats(self) -> dict[str, Any]:
        # Keys and order match ScanStats.to_dict() so the payload is
        # interchangeable with a real scan's on the frontend.
        return {
            "total_platforms": 1,
            "total_roms": self.total,
            "scanned_platforms": 1,
            "new_platforms": 0,
            "identified_platforms": 1 if self._platform.is_identified else 0,
            "scanned_roms": self.scanned,
            "new_roms": self.new,
            "identified_roms": self.identified,
            "scanned_firmware": 0,
            "new_firmware": 0,
            "updated_roms": 0,
            "new_files": 0,
        }

    async def start(self) -> None:
        """Announce the platform being synced (shows the indicator immediately)."""
        try:
            await self._manager().emit(
                "scan:scanning_platform",
                {
                    **PlatformSchema.model_validate(self._platform).model_dump(
                        include={
                            "id",
                            "name",
                            "display_name",
                            "slug",
                            "fs_slug",
                            "is_identified",
                        }
                    ),
                    "new_firmware_count": 0,
                },
            )
        except Exception:  # noqa: BLE001 - progress is best-effort
            pass

    def add_total(self, n: int) -> None:
        self.total += n

    async def rom_done(self, rom: Rom | None, *, is_new: bool) -> None:
        """Record one processed game and push progress to the clients."""
        self.scanned += 1
        if is_new:
            self.new += 1
        if rom is not None and rom.is_identified:
            self.identified += 1
        try:
            manager = self._manager()
            if rom is not None:
                await emit_scanning_rom(manager, rom, is_new=is_new)
            await manager.emit("scan:update_stats", self._stats())
        except Exception:  # noqa: BLE001 - progress is best-effort
            pass

    async def finish(self) -> None:
        """Mark the sync done so the indicator hides and the gallery reconciles."""
        try:
            await self._manager().emit("scan:done", self._stats())
        except Exception:  # noqa: BLE001 - progress is best-effort
            pass
