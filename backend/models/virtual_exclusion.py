from __future__ import annotations

from sqlalchemy import ForeignKey, Index, String, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column

from models.base import BaseModel

EXCLUSION_NAME_MAX_LENGTH = 512


class VirtualExclusion(BaseModel):
    """A game removed with "delete + exclude".

    When a user deletes a virtual game and excludes it, its normalised name is
    recorded per platform so a later list re-import (which dedups by name) never
    recreates it. Only virtual games can be excluded; a physical file on disk is
    never touched by this.
    """

    __tablename__ = "virtual_exclusions"

    __table_args__ = (
        UniqueConstraint("platform_id", "name", name="uq_virtual_exclusion"),
        Index("ix_virtual_exclusions_platform_id", "platform_id"),
    )

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    platform_id: Mapped[int] = mapped_column(
        ForeignKey("platforms.id", ondelete="CASCADE"), nullable=False
    )
    # The game's fs_name without extension (compute_file_name_no_ext), matching
    # how the importer dedups, so exclusion and dedup speak the same key.
    name: Mapped[str] = mapped_column(String(length=EXCLUSION_NAME_MAX_LENGTH))
