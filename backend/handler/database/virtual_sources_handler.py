from typing import Any

from sqlalchemy import delete, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from decorators.database import INJECTED_SESSION, begin_session
from models.base import utc_now
from models.rom import Rom
from models.virtual_rom_mirror import VirtualRomMirror
from models.virtual_rom_source import VirtualRomSource

from .base_handler import DBBaseHandler


class DBVirtualSourcesHandler(DBBaseHandler):
    @begin_session
    def add_source(
        self,
        source: VirtualRomSource,
        session: Session = INJECTED_SESSION,
    ) -> VirtualRomSource:
        source = session.merge(source)
        session.flush()
        return session.scalars(
            select(VirtualRomSource).filter_by(id=source.id).limit(1)
        ).one()

    @begin_session
    def get_source(
        self, id: int, session: Session = INJECTED_SESSION
    ) -> VirtualRomSource | None:
        return session.scalar(select(VirtualRomSource).filter_by(id=id).limit(1))

    @begin_session
    def get_sources(
        self,
        platform_id: int | None = None,
        enabled_only: bool = False,
        session: Session = INJECTED_SESSION,
    ) -> list[VirtualRomSource]:
        query = select(VirtualRomSource)
        if platform_id is not None:
            query = query.filter_by(platform_id=platform_id)
        if enabled_only:
            query = query.filter_by(enabled=True)
        return list(session.scalars(query.order_by(VirtualRomSource.id.asc())).all())

    @begin_session
    def update_source(
        self, id: int, data: dict[str, Any], session: Session = INJECTED_SESSION
    ) -> None:
        source = session.scalar(select(VirtualRomSource).filter_by(id=id).limit(1))
        if source is None:
            return
        for key, value in data.items():
            setattr(source, key, value)
        session.flush()

    @begin_session
    def mark_synced(
        self, id: int, result: str, session: Session = INJECTED_SESSION
    ) -> None:
        self.update_source(
            id,
            {"last_synced_at": utc_now(), "last_result": result},
            session=session,
        )

    @begin_session
    def delete_source(self, id: int, session: Session = INJECTED_SESSION) -> None:
        session.execute(delete(VirtualRomSource).where(VirtualRomSource.id == id))

    # -- mirrors (a game's extra download locations) --------------------------

    @begin_session
    def find_virtual_rom(
        self, platform_id: int, fs_name: str, session: Session = INJECTED_SESSION
    ) -> Rom | None:
        """A still-virtual rom with this file name on the platform, if any.

        Used to dedupe a game listed by more than one source: the second source
        adds a download mirror instead of creating a duplicate row.
        """
        return session.scalar(
            select(Rom)
            .filter_by(platform_id=platform_id, fs_name=fs_name, is_virtual=True)
            .limit(1)
        )

    @begin_session
    def add_mirror(
        self,
        rom_id: int,
        url: str,
        source_id: int | None = None,
        session: Session = INJECTED_SESSION,
    ) -> bool:
        """Add a download mirror for a rom. Returns False if it already had it."""
        exists = session.scalar(
            select(VirtualRomMirror.id)
            .filter_by(rom_id=rom_id, url=url)
            .limit(1)
        )
        if exists is not None:
            return False
        session.add(VirtualRomMirror(rom_id=rom_id, url=url, source_id=source_id))
        try:
            session.flush()
        except IntegrityError:
            session.rollback()
            return False
        return True

    @begin_session
    def get_mirror_urls(
        self, rom_id: int, session: Session = INJECTED_SESSION
    ) -> list[str]:
        return list(
            session.scalars(
                select(VirtualRomMirror.url)
                .filter_by(rom_id=rom_id)
                .order_by(VirtualRomMirror.id.asc())
            ).all()
        )
