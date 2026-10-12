import functools

from config import ESDE_BASE_PATH
from handler.filesystem.base_handler import FSHandler

ESDE_GAMELISTS_DIR = "gamelists"
ESDE_MEDIA_DIR = "downloaded_media"


class FSEsdeHandler(FSHandler):
    def __init__(self) -> None:
        super().__init__(base_path=ESDE_BASE_PATH)


@functools.cache
def get_fs_esde_handler() -> FSEsdeHandler | None:
    """The ES-DE application data folder's handler, or None when not configured."""
    return FSEsdeHandler() if ESDE_BASE_PATH else None
