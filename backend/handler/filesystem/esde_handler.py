import functools
import os
import shutil
from pathlib import Path

from PIL import Image, UnidentifiedImageError

from config import ESDE_BASE_PATH
from handler.filesystem.base_handler import FSHandler
from models.rom import Rom
from utils.filesystem import rel_platform_folder, symlink_file

ESDE_GAMELISTS_DIR = "gamelists"
ESDE_MEDIA_DIR = "downloaded_media"
ESDE_ROMS_DIR = "roms"

# Extensions ES-DE reads, by the format Pillow reports
_IMAGE_EXTENSIONS = {"PNG": ".png", "JPEG": ".jpg", "WEBP": ".webp", "GIF": ".gif"}


def _true_extension(path: Path) -> str | None:
    """The extension matching a file's content, since resources name every image alike."""
    try:
        with Image.open(path) as img:
            return _IMAGE_EXTENSIONS.get(img.format or "")
    except UnidentifiedImageError:
        return path.suffix.lower() or None


class FSEsdeHandler(FSHandler):
    def __init__(self) -> None:
        super().__init__(base_path=ESDE_BASE_PATH)

    def media_name(self, rom: Rom) -> str:
        """A rom's media path below a media type folder, without extension.

        ES-DE names media after the rom inside its system folder, so a rom
        reached through a link belongs to the system the link points into.
        """
        from handler.filesystem import fs_platform_handler, fs_rom_handler

        resolved = fs_rom_handler.validate_path(rom.full_path).resolve()
        name = resolved.name if resolved.is_dir() else resolved.stem
        try:
            folder = resolved.parent.relative_to(self.base_path / ESDE_ROMS_DIR)
        except ValueError:
            folder = Path(
                rom.platform_fs_slug,
                rel_platform_folder(
                    rom.fs_path,
                    fs_platform_handler.get_platform_fs_structure(
                        rom.platform_fs_slug
                    ),
                ),
            )
        system, *subfolders = folder.parts
        return str(Path(system, "{media}", *subfolders, name))

    def adopt_media(self, rom: Rom, source: Path, media_folder: str) -> Path | None:
        """Move a stored file into ES-DE's media folder and link it back.

        Returns:
            The file's new home, or None when it stays where it is: ES-DE
            already has this media for the rom, or its format is unknown.
        """
        if source.is_symlink() or not source.is_file():
            return None
        extension = _true_extension(source)
        if not extension:
            return None

        stem = self.validate_path(
            os.path.join(
                ESDE_MEDIA_DIR, self.media_name(rom).format(media=media_folder)
            )
        )
        stem.parent.mkdir(parents=True, exist_ok=True)
        # Any extension counts, as ES-DE shows whichever it finds first
        if any(
            entry.name == stem.name or entry.stem == stem.name
            for entry in stem.parent.iterdir()
        ):
            return None

        target = stem.with_name(f"{stem.name}{extension}")
        staged = target.with_name(f".romm_tmp_{target.name}")
        try:
            shutil.copyfile(source, staged)
            os.replace(staged, target)
        except BaseException:
            staged.unlink(missing_ok=True)
            raise
        symlink_file(target, source)
        return target


@functools.cache
def get_fs_esde_handler() -> FSEsdeHandler | None:
    """The ES-DE application data folder's handler, or None when not configured."""
    return FSEsdeHandler() if ESDE_BASE_PATH else None
