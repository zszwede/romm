from __future__ import annotations

from sqlalchemy import ForeignKey, Index, String, UniqueConstraint
from sqlalchemy.orm import Mapped, mapped_column

from models.base import BaseModel

MIRROR_URL_MAX_LENGTH = 1024


class VirtualRomMirror(BaseModel):
    """An extra download location for a virtual ROM.

    A game listed by two sources on the same platform is one `Rom` with several
    mirrors, not two rows. Mirrors are also tried as fallbacks when a download
    fails (archive.org nodes 500 intermittently).
    """

    __tablename__ = "virtual_rom_mirrors"

    __table_args__ = (
        UniqueConstraint("rom_id", "url", name="uq_virtual_rom_mirror"),
        Index("ix_virtual_rom_mirrors_rom_id", "rom_id"),
    )

    id: Mapped[int] = mapped_column(primary_key=True, autoincrement=True)
    rom_id: Mapped[int] = mapped_column(
        ForeignKey("roms.id", ondelete="CASCADE"), nullable=False
    )
    url: Mapped[str] = mapped_column(String(length=MIRROR_URL_MAX_LENGTH))
    # Which source contributed this mirror (nullable: the source may be deleted).
    source_id: Mapped[int | None] = mapped_column(
        ForeignKey("virtual_rom_sources.id", ondelete="SET NULL"), default=None
    )
