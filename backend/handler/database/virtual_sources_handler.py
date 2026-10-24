from typing import Any

from sqlalchemy import delete, or_, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from decorators.database import INJECTED_SESSION, begin_session
from models.base import utc_now
from models.collection import CollectionRom
from models.rom import Rom
from models.virtual_exclusion import VirtualExclusion
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
    def find_existing_rom(
        self,
        platform_id: int,
        fs_name: str,
        fs_name_no_ext: str,
        session: Session = INJECTED_SESSION,
    ) -> Rom | None:
        """An existing rom this source's game should attach to as a mirror.

        Matches a rom on the platform with the same file name, or the same name
        ignoring the archive extension (so a game offered as ``Game.zip`` by one
        source and ``Game.7z`` by another dedupes to one row). A PHYSICAL match
        wins over a virtual one, so a game already in the library becomes an
        extra download source on the local file rather than a virtual duplicate
        — the local file keeps precedence. Returns None when nothing matches,
        in which case the caller creates a new virtual rom.

        Deliberately NOT keyed on metadata id: roms that merely share an
        igdb/ss id are sibling *versions* (regions/revisions) and must stay
        separate rows, so a name key is used instead.
        """
        return session.scalar(
            select(Rom)
            .where(
                Rom.platform_id == platform_id,
                or_(
                    Rom.fs_name == fs_name,
                    Rom.fs_name_no_ext == fs_name_no_ext,
                ),
            )
            # Physical (is_virtual False) before virtual, then lowest id, so the
            # keeper is deterministic and prefers a real local file.
            .order_by(Rom.is_virtual.asc(), Rom.id.asc())
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

    @begin_session
    def remove_source_url(
        self, rom_id: int, url: str, session: Session = INJECTED_SESSION
    ) -> bool:
        """Drop one download source from a rom.

        Removes a matching mirror row; if the url is the rom's primary
        ``source_url``, the first remaining mirror is promoted to primary (and
        its mirror row dropped) so the rom keeps a usable source. Returns True
        when something changed.
        """
        rom = session.scalar(select(Rom).filter_by(id=rom_id).limit(1))
        if rom is None:
            return False

        changed = bool(
            session.execute(
                delete(VirtualRomMirror).where(
                    VirtualRomMirror.rom_id == rom_id, VirtualRomMirror.url == url
                )
            ).rowcount
        )

        if rom.source_url == url:
            promoted = session.scalar(
                select(VirtualRomMirror)
                .filter_by(rom_id=rom_id)
                .order_by(VirtualRomMirror.id.asc())
                .limit(1)
            )
            rom.source_url = promoted.url if promoted is not None else None
            if promoted is not None:
                session.delete(promoted)
            changed = True

        if changed:
            session.flush()
        return changed

    @begin_session
    def merge_roms(
        self, keeper_id: int, absorbed_id: int, session: Session = INJECTED_SESSION
    ) -> Rom | None:
        """Fold ``absorbed`` into ``keeper``: move its download sources over as
        mirrors, move its collection memberships, then delete it.

        The caller guarantees ``absorbed`` is virtual (never has a local file),
        so nothing on disk is lost. ``keeper`` is whichever row should remain —
        for a virtual+physical pair that is the physical one, so the local file
        keeps precedence and the archive.org links become fallback mirrors.
        Returns the keeper rom, or None if either row is gone.
        """
        keeper = session.scalar(select(Rom).filter_by(id=keeper_id).limit(1))
        absorbed = session.scalar(select(Rom).filter_by(id=absorbed_id).limit(1))
        if keeper is None or absorbed is None or keeper_id == absorbed_id:
            return None

        # Every download source the absorbed row carried (primary + mirrors)
        # becomes a mirror on the keeper, skipping ones it already has.
        existing_urls = {m.url for m in keeper.virtual_mirrors}
        if keeper.source_url:
            existing_urls.add(keeper.source_url)
        for url in absorbed.source_urls:
            if url and url not in existing_urls:
                session.add(VirtualRomMirror(rom_id=keeper_id, url=url))
                existing_urls.add(url)

        # Move the absorbed row's collection memberships to the keeper. The
        # (collection_id, rom_id) pair is the composite primary key, so rather
        # than mutate it in place we drop the absorbed memberships and add the
        # keeper to any collection it was not already in.
        keeper_collection_ids = {
            cid
            for (cid,) in session.execute(
                select(CollectionRom.collection_id).where(
                    CollectionRom.rom_id == keeper_id
                )
            ).all()
        }
        absorbed_collection_ids = {
            cid
            for (cid,) in session.execute(
                select(CollectionRom.collection_id).where(
                    CollectionRom.rom_id == absorbed_id
                )
            ).all()
        }
        session.execute(
            delete(CollectionRom).where(CollectionRom.rom_id == absorbed_id)
        )
        for cid in absorbed_collection_ids - keeper_collection_ids:
            session.add(CollectionRom(collection_id=cid, rom_id=keeper_id))

        session.flush()
        # Bulk Core delete (not session.delete) so the DB's ON DELETE CASCADE
        # clears the absorbed row's children — matching delete_rom. An ORM
        # delete would instead try to blank child FKs and trip on roms_metadata.
        session.execute(
            delete(Rom)
            .where(Rom.id == absorbed_id)
            .execution_options(synchronize_session="evaluate")
        )
        session.flush()
        session.expire(keeper)
        return session.scalar(select(Rom).filter_by(id=keeper_id).limit(1))

    @begin_session
    def add_exclusion(
        self, platform_id: int, name: str, session: Session = INJECTED_SESSION
    ) -> bool:
        """Record a (platform, name) so a future import won't recreate it.
        Returns False if it was already excluded."""
        exists = session.scalar(
            select(VirtualExclusion.id)
            .filter_by(platform_id=platform_id, name=name)
            .limit(1)
        )
        if exists is not None:
            return False
        session.add(VirtualExclusion(platform_id=platform_id, name=name))
        try:
            session.flush()
        except IntegrityError:
            session.rollback()
            return False
        return True

    @begin_session
    def is_excluded(
        self, platform_id: int, name: str, session: Session = INJECTED_SESSION
    ) -> bool:
        return (
            session.scalar(
                select(VirtualExclusion.id)
                .filter_by(platform_id=platform_id, name=name)
                .limit(1)
            )
            is not None
        )

    @begin_session
    def excluded_names(
        self, platform_id: int, session: Session = INJECTED_SESSION
    ) -> set[str]:
        """All excluded names for a platform, for a bulk import check."""
        return set(
            session.scalars(
                select(VirtualExclusion.name).filter_by(platform_id=platform_id)
            ).all()
        )

    @begin_session
    def download_queue(
        self,
        statuses: list[str],
        platform_id: int | None = None,
        limit: int = 500,
        session: Session = INJECTED_SESSION,
    ) -> list[Rom]:
        """Virtual roms whose download_status is in `statuses`, oldest first —
        what an external merge service polls to pick up work."""
        stmt = (
            select(Rom)
            .where(Rom.is_virtual.is_(True), Rom.download_status.in_(statuses))
            .order_by(Rom.id.asc())
            .limit(limit)
        )
        if platform_id is not None:
            stmt = stmt.where(Rom.platform_id == platform_id)
        return list(session.scalars(stmt).all())
