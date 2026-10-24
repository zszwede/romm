"""Turn an archive.org index URL into a list of games to add as virtual ROMs.

The admin pastes one of several URL shapes (all seen in the wild):

  * ``https://archive.org/details/<id>``                    an item page
  * ``https://archive.org/download/<id>``                   an item download root
  * ``https://ia600705.us.archive.org/16/items/<id>/``      the same item, CDN node
  * ``https://archive.org/download/<id>/<file>``            one file in an item
  * ``https://archive.org/download/<id>/<zip>/``            contents of a zip
  * ``.../view_archive.php?archive=/NN/items/<id>/<zip>``   contents of a zip

Everything is normalised to ``(identifier, archive_path?)`` and then enumerated:

  * an item lists its top-level ROM / archive files via the public metadata API
    (``https://archive.org/metadata/<id>``, JSON, no auth);
  * an archive lists its members via ``view_archive.php`` (HTML; there is no
    JSON form), and each member is fetched on demand from
    ``download/<id>/<zip>/<member>`` — archive.org extracts a single member, so
    a game can be pulled without downloading the whole set.

The HTTP-free parsing (:func:`parse_source_url`, :func:`games_from_metadata`,
:func:`games_from_archive_listing`) is kept separate from the fetching so it can
be unit-tested against saved fixtures.
"""

from __future__ import annotations

import html
import re
from dataclasses import dataclass
from urllib.parse import parse_qs, unquote, urlparse

import httpx2

from logger.logger import log
from utils.context import ctx_httpx_client

from . import archive_auth

REQUEST_TIMEOUT = 25

# Extensions that are themselves a per-game container on archive.org.
ARCHIVE_EXTENSIONS = frozenset({"zip", "7z", "rar"})

# Files archive.org generates or stores for every item that are never games:
# the metadata manifests (``<id>_files.xml``, ``<id>_meta.xml``,
# ``<id>_reviews.xml``), the metadata DB (``<id>_meta.sqlite``), the torrent
# (``<id>_archive.torrent``) and item imagery (``__ia_thumb.jpg``,
# ``<id>_itemimage.jpg``, scans). The identifier prefix differs per item, so
# these are matched by extension rather than by name. Some items list such
# sidecars with ``source == "original"``, so excluding them here (not only via
# the source filter) is what keeps them out of the sync.
ALWAYS_SKIP_EXTENSIONS = frozenset(
    {
        "torrent",
        "sqlite",
        "xml",
        "json",
        "jpg",
        "jpeg",
        "png",
        "gif",
        "bmp",
        "webp",
    }
)

# archive.org's own image bundle, matched by name because its extension
# (``7z``/``zip``) is otherwise a valid ROM container. ``__ia*`` internal files
# are handled by prefix in ``_keep``.
ALWAYS_SKIP_BASENAMES = frozenset({"images.7z", "images.zip"})

# A lone archive at an item root is only auto-enumerated as a multi-game pack
# when it is at least this big; a small single-game zip is kept as one game.
MULTI_GAME_ARCHIVE_MIN_BYTES = 64 * 1024 * 1024


@dataclass(frozen=True)
class VirtualGame:
    """One game to create as a virtual ROM."""

    name: str  # becomes the rom's fs_name
    source_url: str  # direct archive.org download URL
    size: int | None = None
    # Full downloadable set (base + updates + DLC) for a merge service; each item
    # is {category, name, url, host, version, size}. Empty for a plain single
    # file (the base game is then just `source_url`).
    contents: tuple = ()


def host_of(url: str | None) -> str | None:
    """The bare host of a download URL, normalised for grouping/filtering:
    lowercased with a leading ``www.`` stripped (``https://www.1fichier.com/?x``
    -> ``1fichier.com``). Returns None for a blank or unparseable URL."""
    if not url:
        return None
    try:
        netloc = urlparse(url if "://" in url else f"https://{url}").netloc.lower()
    except ValueError:
        return None
    netloc = netloc.rsplit("@", 1)[-1].split(":", 1)[0]  # drop creds + port
    if netloc.startswith("www."):
        netloc = netloc[4:]
    return netloc or None


def hosts_of(urls) -> list[str]:
    """Distinct, order-preserving hosts for a set of URLs."""
    out: list[str] = []
    for url in urls:
        host = host_of(url)
        if host and host not in out:
            out.append(host)
    return out


@dataclass(frozen=True)
class ParsedSource:
    identifier: str
    # Path of an archive within the item to enumerate; None means the item root.
    archive_path: str | None = None
    # Path of a single, specific file within the item (not an archive).
    file_path: str | None = None


class ArchiveOrgError(Exception):
    """A source URL could not be understood or fetched."""


def _extension(name: str) -> str:
    base = name.rsplit("/", 1)[-1]
    return base.rsplit(".", 1)[-1].lower() if "." in base else ""


def _is_archive(name: str) -> bool:
    return _extension(name) in ARCHIVE_EXTENSIONS


def _download_url(identifier: str, path: str) -> str:
    # `path` is already URL-encoded as it appears in archive.org links.
    return f"https://archive.org/download/{identifier}/{path}"


def parse_source_url(url: str) -> ParsedSource:
    """Normalise any accepted archive.org URL to a :class:`ParsedSource`."""

    url = url.strip()
    parsed = urlparse(url)
    if not parsed.scheme:
        parsed = urlparse("https://" + url)

    # view_archive.php?archive=/NN/items/<id>/<zip>
    if parsed.path.endswith("/view_archive.php"):
        archive = parse_qs(parsed.query).get("archive", [""])[0]
        m = re.search(r"/items/([^/]+)/(.+)$", archive)
        if not m:
            raise ArchiveOrgError(f"cannot parse view_archive URL: {url}")
        return ParsedSource(identifier=unquote(m.group(1)), archive_path=m.group(2))

    path = parsed.path

    # CDN node URL: /NN/items/<id>/<rest>
    m = re.search(r"/items/([^/]+)/?(.*)$", path)
    if m:
        return _classify(unquote(m.group(1)), m.group(2))

    # archive.org/details/<id>
    m = re.match(r"^/details/([^/]+)/?$", path)
    if m:
        return ParsedSource(identifier=unquote(m.group(1)))

    # archive.org/download/<id>[/<rest>]
    m = re.match(r"^/download/([^/]+)/?(.*)$", path)
    if m:
        return _classify(unquote(m.group(1)), m.group(2))

    raise ArchiveOrgError(f"not a recognised archive.org URL: {url}")


def _classify(identifier: str, rest: str) -> ParsedSource:
    """Decide whether `rest` names an item root, an archive, or a single file."""
    rest = rest.strip("/")  # a trailing slash on a zip means "list its contents"
    if not rest:
        return ParsedSource(identifier=identifier)
    if _is_archive(rest):
        return ParsedSource(identifier=identifier, archive_path=rest)
    return ParsedSource(identifier=identifier, file_path=rest)


def _keep(name: str, rom_extensions: frozenset[str]) -> bool:
    base = name.rsplit("/", 1)[-1].lower()
    # archive.org internal files (e.g. __ia_thumb.jpg) and its image bundles.
    if base.startswith("__ia") or base in ALWAYS_SKIP_BASENAMES:
        return False
    ext = _extension(name)
    if not ext or ext in ALWAYS_SKIP_EXTENSIONS:
        return False
    if ext in ARCHIVE_EXTENSIONS:
        return True
    return ext in rom_extensions if rom_extensions else True


def games_from_metadata(
    meta: dict, identifier: str, rom_extensions: frozenset[str]
) -> tuple[list[VirtualGame], str | None]:
    """Games from an item's metadata-API JSON.

    Returns ``(games, archive_to_enumerate)``: when the item's only ROM content
    is a single large archive, `games` is empty and the second value is that
    archive's path, which the caller should enumerate instead.
    """
    originals = [
        f
        for f in meta.get("files", [])
        # "original" excludes archive.org's own *_meta.xml / derivatives.
        if f.get("source") == "original" and f.get("name")
    ]
    kept = [f for f in originals if _keep(f["name"], rom_extensions)]

    if (
        len(kept) == 1
        and _is_archive(kept[0]["name"])
        and int(kept[0].get("size") or 0) >= MULTI_GAME_ARCHIVE_MIN_BYTES
    ):
        return [], kept[0]["name"]

    games = [
        VirtualGame(
            name=f["name"].rsplit("/", 1)[-1],
            source_url=_download_url(identifier, _encode_path(f["name"])),
            size=int(f["size"]) if f.get("size") else None,
        )
        for f in kept
    ]
    return games, None


# view_archive.php rows: <a href="//archive.org/download/..">name</a> … <td id="size">N
_MEMBER_ROW = re.compile(
    r'<a href="(//archive\.org/download/[^"]+)">([^<]*)</a>'
    r".*?<td id=\"size\">(\d+)",
    re.DOTALL,
)


def games_from_archive_listing(
    listing_html: str, rom_extensions: frozenset[str]
) -> list[VirtualGame]:
    """Games from a ``view_archive.php`` HTML listing of a zip's members."""
    games: list[VirtualGame] = []
    for href, display, size in _MEMBER_ROW.findall(listing_html):
        name = html.unescape(display).rsplit("/", 1)[-1].strip()
        if not name or not _keep(name, rom_extensions):
            continue
        games.append(
            VirtualGame(
                name=name,
                source_url="https:" + href,
                size=int(size) if size else None,
            )
        )
    return games


def _encode_path(path: str) -> str:
    from urllib.parse import quote

    return quote(path)


async def _get(url: str) -> httpx2.Response:
    client = ctx_httpx_client.get()
    # Follow redirects through archive_auth so access-restricted items (which
    # 302 to a CDN node) keep their auth header across the cross-host hop.
    response = await archive_auth.send_following(
        client, "GET", url, timeout=REQUEST_TIMEOUT
    )
    response.raise_for_status()
    return response


async def list_games(
    url: str, rom_extensions: frozenset[str]
) -> tuple[list[VirtualGame], str]:
    """Fetch and enumerate the games a source URL points at.

    Returns ``(games, kind)`` where kind is ``"item"`` or ``"archive"``.
    """
    source = parse_source_url(url)

    if source.file_path:
        name = unquote(source.file_path).rsplit("/", 1)[-1]
        return (
            [
                VirtualGame(
                    name=name,
                    source_url=_download_url(source.identifier, source.file_path),
                )
            ],
            "item",
        )

    archive_path = source.archive_path
    if archive_path is None:
        meta = (await _get(f"https://archive.org/metadata/{source.identifier}")).json()
        games, to_enumerate = games_from_metadata(
            meta, source.identifier, rom_extensions
        )
        if to_enumerate is None:
            log.info(
                "archive.org item %s: %d games", source.identifier, len(games)
            )
            return games, "item"
        archive_path = to_enumerate

    # Enumerate the members of an archive within the item.
    listing = (
        await _get(f"{_download_url(source.identifier, _encode_path(archive_path))}/")
    ).text
    games = games_from_archive_listing(listing, rom_extensions)
    log.info(
        "archive.org %s/%s: %d members", source.identifier, archive_path, len(games)
    )
    return games, "archive"
