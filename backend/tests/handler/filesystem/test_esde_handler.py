from pathlib import Path
from types import SimpleNamespace
from typing import cast
from unittest.mock import patch

import pytest
from PIL import Image

from handler.filesystem import fs_platform_handler, fs_rom_handler
from handler.filesystem.esde_handler import FSEsdeHandler
from models.rom import Rom


@pytest.fixture
def esde(tmp_path: Path):
    """An ES-DE data folder beside a library, with both handlers pointed at them."""
    library = tmp_path / "library"
    (library / "roms").mkdir(parents=True)
    esde_root = tmp_path / "esde"
    (esde_root / "roms").mkdir(parents=True)

    with (
        patch("handler.filesystem.esde_handler.ESDE_BASE_PATH", str(esde_root)),
        patch.object(fs_rom_handler, "base_path", library),
        patch.object(
            fs_platform_handler,
            "get_platform_fs_structure",
            side_effect=lambda fs_slug: f"roms/{fs_slug}",
        ),
    ):
        yield SimpleNamespace(handler=FSEsdeHandler(), library=library, root=esde_root)


def _rom(fs_slug: str, fs_name: str, fs_path: str | None = None) -> Rom:
    fs_path = fs_path or f"roms/{fs_slug}"
    return cast(
        Rom,
        SimpleNamespace(
            platform_fs_slug=fs_slug,
            fs_path=fs_path,
            fs_name=fs_name,
            full_path=f"{fs_path}/{fs_name}",
        ),
    )


def _stored_image(tmp_path: Path, image_format: str = "JPEG") -> Path:
    stored = tmp_path / "resources" / "cover" / "big.png"
    stored.parent.mkdir(parents=True)
    Image.new("RGB", (8, 8)).save(stored, format=image_format)
    return stored


def test_adopt_media_uses_the_system_a_linked_rom_points_into(esde, tmp_path: Path):
    real_rom = esde.root / "roms" / "amiga500" / "Zool.lha"
    real_rom.parent.mkdir()
    real_rom.touch()
    merged = esde.library / "roms" / "amiga"
    merged.mkdir()
    (merged / "Zool (amiga500).lha").symlink_to(real_rom)
    stored = _stored_image(tmp_path)

    target = esde.handler.adopt_media(
        _rom("amiga", "Zool (amiga500).lha"), stored, "covers"
    )

    assert target == esde.root / "downloaded_media" / "amiga500" / "covers" / "Zool.jpg"
    assert stored.is_symlink()
    assert stored.resolve() == target


def test_adopt_media_keeps_the_subfolder_of_a_library_rom(esde, tmp_path: Path):
    rom_dir = esde.library / "roms" / "psx" / "RPG"
    rom_dir.mkdir(parents=True)
    (rom_dir / "Xenogears (USA)").mkdir()
    stored = _stored_image(tmp_path, "PNG")

    target = esde.handler.adopt_media(
        _rom("psx", "Xenogears (USA)", "roms/psx/RPG"), stored, "screenshots"
    )

    assert target == (
        esde.root / "downloaded_media/psx/screenshots/RPG/Xenogears (USA).png"
    )


def test_adopt_media_leaves_media_esde_already_has(esde, tmp_path: Path):
    (esde.library / "roms" / "psx").mkdir()
    (esde.library / "roms" / "psx" / "Game.chd").touch()
    existing = esde.root / "downloaded_media" / "psx" / "covers" / "Game.png"
    existing.parent.mkdir(parents=True)
    existing.write_bytes(b"original")
    stored = _stored_image(tmp_path)

    assert esde.handler.adopt_media(_rom("psx", "Game.chd"), stored, "covers") is None

    assert existing.read_bytes() == b"original"
    assert not stored.is_symlink()
    assert [p.name for p in existing.parent.iterdir()] == ["Game.png"]


def test_adopt_media_keeps_a_manuals_extension(esde, tmp_path: Path):
    (esde.library / "roms" / "psx").mkdir()
    (esde.library / "roms" / "psx" / "Game.chd").touch()
    stored = tmp_path / "12.pdf"
    stored.write_bytes(b"%PDF-1.4")

    target = esde.handler.adopt_media(_rom("psx", "Game.chd"), stored, "manuals")

    assert target == esde.root / "downloaded_media" / "psx" / "manuals" / "Game.pdf"
    assert stored.read_bytes() == b"%PDF-1.4"


def test_adopt_media_skips_a_linked_file(esde, tmp_path: Path):
    (esde.library / "roms" / "psx").mkdir()
    (esde.library / "roms" / "psx" / "Game.chd").touch()
    original = _stored_image(tmp_path)
    linked = tmp_path / "linked.png"
    linked.symlink_to(original)

    assert esde.handler.adopt_media(_rom("psx", "Game.chd"), linked, "covers") is None
    assert not (esde.root / "downloaded_media").exists()
