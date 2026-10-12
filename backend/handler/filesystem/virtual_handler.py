import os

from config import VIRTUAL_DOWNLOAD_BASE_PATH
from config.config_manager import PLATFORM_MEDIA_DIRS

from .base_handler import FSHandler
from .esde_handler import ESDE_GAMELISTS_DIR, ESDE_MEDIA_DIR, ESDE_ROMS_DIR


class FSVirtualHandler(FSHandler):
    """Writes for the virtual-ROM feature (folder creation and downloads).

    Rooted at a writable base (`VIRTUAL_DOWNLOAD_BASE_PATH`) so it works even when
    the normal library is read-only, e.g. an ES-DE overlay. The layout mirrors
    ES-DE: `roms/<slug>`, `downloaded_media/<slug>/<folder>`, `gamelists/<slug>`.
    """

    def __init__(self) -> None:
        super().__init__(base_path=VIRTUAL_DOWNLOAD_BASE_PATH)

    def roms_dir(self, fs_slug: str) -> str:
        return f"{ESDE_ROMS_DIR}/{fs_slug}"

    def rom_write_path(self, fs_slug: str, fs_name: str) -> str:
        """Absolute path a downloaded game is written to (under the base)."""
        return str(self.validate_path(f"{self.roms_dir(fs_slug)}/{fs_name}"))

    async def ensure_platform_dirs(self, fs_slug: str) -> None:
        """Create the roms + media + gamelist folders for a platform.

        Called when archive.org links are assigned to a platform, so a brand-new
        platform (e.g. Apple II) lands with the whole structure in place.
        """
        await self.make_directory(self.roms_dir(fs_slug))
        await self.make_directory(f"{ESDE_GAMELISTS_DIR}/{fs_slug}")
        for folder in sorted(set(PLATFORM_MEDIA_DIRS.values())):
            await self.make_directory(f"{ESDE_MEDIA_DIR}/{fs_slug}/{folder}")

    def base_is_library(self) -> bool:
        from config import LIBRARY_BASE_PATH

        return os.path.normpath(VIRTUAL_DOWNLOAD_BASE_PATH) == os.path.normpath(
            LIBRARY_BASE_PATH
        )


fs_virtual_handler = FSVirtualHandler()
