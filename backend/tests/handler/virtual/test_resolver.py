"""Unit tests for the virtual-source download resolver's classification."""

import pytest

from handler.virtual.resolver import (
    BROWSER_FLOWS,
    ParsedURL,
    ResolveError,
    parse_url,
    source_kind,
)


@pytest.mark.parametrize(
    "url,expected",
    [
        # direct: archive.org / github / plain file extension
        ("https://archive.org/download/ps4-fpkg/Game%20(EU).pkg", "direct"),
        ("https://github.com/x/y/releases/download/v1/Game.zip", "direct"),
        ("https://example.com/path/Game.exfat", "direct"),
        ("https://host.tld/x/Game.ffpfsc", "direct"),
        # file hosts behind a captcha/Cloudflare gate -> their own flow
        ("https://www.mediafire.com/file/abc/Game.pkg/file", "mediafire"),
        ("https://vikingfile.com/f/abc123", "vikingfile"),
        ("https://buzzheavier.com/abc", "buzzheavier"),
        ("https://datanodes.to/abc", "datanodes"),
        ("https://filekeeper.net/aabbccddeeff", "filekeeper"),
        # host-specific direct shortcuts
        ("https://download1234.mediafire.com/xyz/Game.pkg", "direct"),
        ("https://vikingfile.com/d/abc123", "direct"),
        # known captcha hosts classify as browser by host, no flag needed
        ("https://1fichier.com/?abcdef", "browser"),
        ("https://gofile.io/d/abc", "browser"),
        ("https://datavaults.co/abc", "browser"),
        ("https://rapidgator.net/file/abc/Game.pkg.html", "browser"),
    ],
)
def test_source_kind(url, expected):
    # these are recognised without the requiresBrowser flag
    assert source_kind(url, requires_browser=False) == expected


def test_browser_flows_membership():
    assert "browser" in BROWSER_FLOWS
    assert {"datanodes", "buzzheavier", "akirabox"} <= BROWSER_FLOWS
    assert "direct" not in BROWSER_FLOWS
    assert "mediafire" not in BROWSER_FLOWS  # auto-resolvable


def test_parse_url_ok():
    p = parse_url("https://archive.org/download/x/Game.pkg?a=1")
    assert isinstance(p, ParsedURL)
    assert p.host == "archive.org"
    assert p.origin == "https://archive.org"
    assert p.path == "/download/x/Game.pkg?a=1"


@pytest.mark.parametrize(
    "bad",
    [
        "ftp://example.com/x",
        "not a url",
        "https://.example.com/x",
        "https://example.com/\x00",
    ],
)
def test_parse_url_rejects(bad):
    with pytest.raises(ResolveError):
        parse_url(bad)
