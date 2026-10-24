import os

from config import VIRTUAL_DOWNLOAD_BASE_PATH
from config.config_manager import PLATFORM_MEDIA_DIRS

from .base_handler import FSHandler
from .esde_handler import ESDE_GAMELISTS_DIR, ESDE_MEDIA_DIR, ESDE_ROMS_DIR

# RomM's canonical platform slug differs from the ES-DE on-disk folder for a few
# systems (platform_aliases.py maps the ES-DE folder -> RomM slug on *read*).
# The DB stores the slug as fs_path while the files physically live in the ES-DE
# folder (e.g. fs_path "roms/amiga-cd32", file in roms/amigacd32). Virtual
# downloads must therefore WRITE to the ES-DE folder, while the stored fs_path
# stays the slug — otherwise the library splits into a slug folder ES-DE can't
# see. Only the physical write path is remapped here.
_ESDE_FOLDER_FOR_SLUG = {
    "amiga-cd32": "amigacd32",
    "commodore-cdtv": "amigacdtv",
    "dc": "dreamcast",
    "neogeoaes": "neogeo",
}


def esde_folder(fs_slug: str) -> str:
    """The ES-DE on-disk folder name for a RomM platform slug."""
    return _ESDE_FOLDER_FOR_SLUG.get(fs_slug, fs_slug)


class FSVirtualHandler(FSHandler):
    """Writes for the virtual-ROM feature (folder creation and downloads).

    Rooted at a writable base (`VIRTUAL_DOWNLOAD_BASE_PATH`) so it works even when
    the normal library is read-only, e.g. an ES-DE overlay. The layout mirrors
    ES-DE: `roms/<slug>`, `downloaded_media/<slug>/<folder>`, `gamelists/<slug>`.
    """

    def __init__(self) -> None:
        super().__init__(base_path=VIRTUAL_DOWNLOAD_BASE_PATH)

    def roms_dir(self, fs_slug: str) -> str:
        """Library-relative roms path stored as a rom's fs_path (the SLUG)."""
        return f"{ESDE_ROMS_DIR}/{fs_slug}"

    def _write_roms_dir(self, fs_slug: str) -> str:
        """Physical roms folder a download is written to (the ES-DE folder)."""
        return f"{ESDE_ROMS_DIR}/{esde_folder(fs_slug)}"

    def rom_write_path(self, fs_slug: str, fs_name: str) -> str:
        """Absolute path a downloaded game is written to (under the base)."""
        return str(self.validate_path(f"{self._write_roms_dir(fs_slug)}/{fs_name}"))

    async def ensure_platform_dirs(self, fs_slug: str) -> None:
        """Create the roms + media + gamelist folders for a platform.

        Uses the ES-DE folder name so a brand-new platform lands where ES-DE and
        the scanner look, not in a slug folder they ignore.
        """
        folder = esde_folder(fs_slug)
        await self.make_directory(self._write_roms_dir(fs_slug))
        await self.make_directory(f"{ESDE_GAMELISTS_DIR}/{folder}")
        for media in sorted(set(PLATFORM_MEDIA_DIRS.values())):
            await self.make_directory(f"{ESDE_MEDIA_DIR}/{folder}/{media}")

    def base_is_library(self) -> bool:
        from config import LIBRARY_BASE_PATH

        return os.path.normpath(VIRTUAL_DOWNLOAD_BASE_PATH) == os.path.normpath(
            LIBRARY_BASE_PATH
        )


fs_virtual_handler = FSVirtualHandler()
