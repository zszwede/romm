import json
from pathlib import Path

import pytest

from handler.virtual.archive_org import (
    ArchiveOrgError,
    ParsedSource,
    games_from_archive_listing,
    games_from_metadata,
    parse_source_url,
)

FIXTURES = Path(__file__).parent / "fixtures"
# A representative set of ROM extensions; .zip is always accepted as a container.
ROM_EXTS = frozenset({"a52", "atr", "xex", "car", "bin", "sfc", "smc"})


class TestParseSourceUrl:
    def test_item_download_root(self):
        assert parse_source_url(
            "https://archive.org/download/atari_2600_rom_collection"
        ) == ParsedSource(identifier="atari_2600_rom_collection")

    def test_item_download_root_trailing_slash(self):
        assert parse_source_url(
            "https://archive.org/download/sega-dreamcast-redump-collection/"
        ) == ParsedSource(identifier="sega-dreamcast-redump-collection")

    def test_details_page(self):
        assert parse_source_url(
            "https://archive.org/details/SNESROMS_201810"
        ) == ParsedSource(identifier="SNESROMS_201810")

    def test_cdn_node_items_url(self):
        assert parse_source_url(
            "https://ia800705.us.archive.org/16/items/Atari800RomCollectionByGhostware/"
        ) == ParsedSource(identifier="Atari800RomCollectionByGhostware")

    def test_specific_archive_file(self):
        assert parse_source_url(
            "https://archive.org/download/SNESROMS_201810/SNES_ROMS.zip"
        ) == ParsedSource(identifier="SNESROMS_201810", archive_path="SNES_ROMS.zip")

    def test_archive_trailing_slash_is_still_an_archive(self):
        assert parse_source_url(
            "https://archive.org/download/SNESROMS_201810/SNES_ROMS.zip/"
        ) == ParsedSource(identifier="SNESROMS_201810", archive_path="SNES_ROMS.zip")

    def test_specific_single_rom_file(self):
        assert parse_source_url(
            "https://archive.org/download/some_item/Game (USA).a52"
        ) == ParsedSource(identifier="some_item", file_path="Game (USA).a52")

    def test_view_archive_php(self):
        assert parse_source_url(
            "https://ia803105.us.archive.org/view_archive.php"
            "?archive=/20/items/SNESROMS_201810/SNES_ROMS.zip"
        ) == ParsedSource(identifier="SNESROMS_201810", archive_path="SNES_ROMS.zip")

    def test_url_without_scheme(self):
        assert parse_source_url(
            "archive.org/download/atari_2600_rom_collection"
        ) == ParsedSource(identifier="atari_2600_rom_collection")

    def test_unrecognised_url(self):
        with pytest.raises(ArchiveOrgError):
            parse_source_url("https://example.com/not/archive")


class TestGamesFromMetadata:
    def _meta(self):
        return json.loads((FIXTURES / "metadata_item.json").read_text())

    def test_per_game_files_become_games(self):
        games, enumerate_archive = games_from_metadata(
            self._meta(), "Atari800RomCollectionByGhostware", ROM_EXTS
        )
        assert enumerate_archive is None
        names = {g.name for g in games}
        # Four .zip games, including the one in a subdir (basename only).
        assert names == {
            "Grad Nord.zip",
            "007 - The Living Daylights (USA).zip",
            "Boulder Dash (Europe).zip",
            "180 (Japan).zip",
        }

    def test_sidecar_and_derivative_files_are_skipped(self):
        games, _ = games_from_metadata(
            self._meta(), "Atari800RomCollectionByGhostware", ROM_EXTS
        )
        names = {g.name for g in games}
        assert not any(n.endswith((".xml", ".torrent", ".jpg")) for n in names)

    def test_download_url_is_encoded(self):
        games, _ = games_from_metadata(
            self._meta(), "Atari800RomCollectionByGhostware", ROM_EXTS
        )
        url = next(g.source_url for g in games if g.name == "Boulder Dash (Europe).zip")
        assert url == (
            "https://archive.org/download/Atari800RomCollectionByGhostware/"
            "Boulder%20Dash%20%28Europe%29.zip"
        )

    def test_single_large_archive_signals_enumeration(self):
        meta = {
            "files": [
                {"name": "SNES_ROMS.zip", "source": "original", "size": "659711336"},
                {"name": "x_meta.xml", "source": "metadata", "size": "900"},
            ]
        }
        games, enumerate_archive = games_from_metadata(meta, "SNESROMS_201810", ROM_EXTS)
        assert games == []
        assert enumerate_archive == "SNES_ROMS.zip"

    def test_single_small_archive_is_one_game(self):
        meta = {"files": [{"name": "One Game.zip", "source": "original", "size": "500000"}]}
        games, enumerate_archive = games_from_metadata(meta, "item", ROM_EXTS)
        assert enumerate_archive is None
        assert [g.name for g in games] == ["One Game.zip"]


class TestGamesFromArchiveListing:
    def test_members_parsed_with_sizes(self):
        html = (FIXTURES / "view_archive_snes.html").read_text()
        games = games_from_archive_listing(html, ROM_EXTS)
        first = games[0]
        assert first.name == "3 Ninjas Kick Back.zip"
        assert first.size == 1104088
        assert first.source_url == (
            "https://archive.org/download/SNESROMS_201810/SNES_ROMS.zip/"
            "3%20Ninjas%20Kick%20Back.zip"
        )

    def test_non_rom_members_filtered_out(self):
        html = (FIXTURES / "view_archive_snes.html").read_text()
        games = games_from_archive_listing(html, ROM_EXTS)
        assert not any(g.name == "readme.txt" for g in games)

    def test_html_entities_in_name_unescaped(self):
        html = (FIXTURES / "view_archive_snes.html").read_text()
        games = games_from_archive_listing(html, ROM_EXTS)
        assert any(g.name == "7th Saga, The.zip" for g in games)
