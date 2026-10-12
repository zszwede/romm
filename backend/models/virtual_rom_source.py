from __future__ import annotations

import enum
from datetime import datetime
from typing import TYPE_CHECKING

from sqlalchemy import (
    Boolean,
    ForeignKey,
    Index,
    String,
    Text,
)
from sqlalchemy.orm import Mapped, mapped_column, relationship

from models.base import BaseModel
from utils.database import CustomJSON

if TYPE_CHECKING:
    from models.platform import Platform

# Max length of a stored archive.org source URL.
SOURCE_URL_MAX_LENGTH = 1024


class VirtualSourceKind(enum.StrEnum):
    # A top-level archive.org item: each top-level ROM/archive file is one game.
    ITEM = "item"
    # A single archive (zip/7z) inside an item: each member is one game.
    ARCHIVE = "archive"


class VirtualRomSource(BaseModel):
    """An archive.org index assigned to a platform.

    Syncing a source creates one virtual `Rom` per game it lists. A source may
    point at an item (per-game files) or at an archive within an item (per-game
    members); `kind` is resolved by the parser from the URL and contents.
    """

    __tablename__ = "virtual_rom_sources"

    __table_args__ = (
        Index("ix_virtual_rom_sources_platform_id", "platform_id"),
    )

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    platform_id: Mapped[int] = mapped_column(
        ForeignKey("platforms.id", ondelete="CASCADE"), nullable=False
    )
    url: Mapped[str] = mapped_column(String(length=SOURCE_URL_MAX_LENGTH))
    # One of VirtualSourceKind; stored as a plain string the parser resolves.
    kind: Mapped[str | None] = mapped_column(String(length=16), default=None)
    # Optional region allow-list applied at sync time, e.g. ["USA", "Europe"].
    region_filter: Mapped[list | None] = mapped_column(CustomJSON(), default=None)
    enabled: Mapped[bool] = mapped_column(Boolean(), default=True, nullable=False)
    last_synced_at: Mapped[datetime | None] = mapped_column(default=None)
    # Free-text summary of the last sync (counts or an error), shown in the UI.
    last_result: Mapped[str | None] = mapped_column(Text(), default=None)

    platform: Mapped[Platform] = relationship(lazy="joined")
