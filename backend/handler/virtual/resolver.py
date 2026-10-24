"""Resolve a virtual-ROM source URL to a directly-downloadable file URL.

Ported from the overdrive store's ``download-sources.js`` resolver, which maps
each file-host "flow" to how its real file link is obtained:

* ``direct``     archive.org / github / a plain ``.pkg``/``.zip``/... URL — used
                 as-is (a HEAD confirms it is a file, not an HTML gate).
* ``mediafire``  scrape the ``#downloadButton`` href from the page.
* ``vikingfile`` scrape the ``#download-link`` href from the page.
* ``filekeeper`` wait the 5s countdown, POST ``op=download2``, follow the
                 redirect to the file.
* ``browser`` / ``datanodes`` / ``buzzheavier`` / ``akirabox`` — behind a
                 Cloudflare/captcha gate no server request passes: the caller
                 must open the page in a browser and supply the final URL
                 (``BrowserRequired``). That URL then resolves as ``direct``.

Only standard-library + httpx; no site-specific credentials. Scraping/formats
follow the sites as of the overdrive snapshot and may need updating if a host
changes its page.
"""

from __future__ import annotations

import asyncio
import re
from dataclasses import dataclass

import httpx

_URL_RE = re.compile(r"^(https?)://([a-z0-9.-]+)(:\d{1,5})?([/?#].*)?$", re.I)
_DIRECT_EXT_RE = re.compile(
    r"\.(?:ffpfsc|ffpfs|ffpkg|exfat|pkg|zip|rar|7z)(?:[?#]|$)", re.I
)
_FILEKEEPER_COUNTDOWN_S = 5.0

# host -> flow
_HOST_FLOWS = {
    "mediafire.com": "mediafire",
    "buzzheavier.com": "buzzheavier",
    "vikingfile.com": "vikingfile",
    "vik1ngfile.site": "vikingfile",
    "datanodes.to": "datanodes",
    "filekeeper.net": "filekeeper",
}

# Flows that no server request can pass (captcha / Cloudflare): the user must
# open the page in a browser and hand back the final file URL.
BROWSER_FLOWS = frozenset({"browser", "datanodes", "buzzheavier", "akirabox"})

# Hosts whose landing page is a captcha / Cloudflare / wait gate (overdrive's
# ``requiresBrowser`` group) with no server-side auto-resolution — classified as
# "browser" by host, since the catalog's flag is not stored on our side.
BROWSER_HOSTS = frozenset(
    {
        "1fichier.com",
        "filecrypt.cc",
        "gofile.io",
        "ranoz.gg",
        "rapidgator.net",
        "link-vault.org",
        "datavaults.co",
        "akirabox.com",
    }
)


class ResolveError(Exception):
    """A source is permanently unusable (404/410/bad response)."""


class BrowserRequired(Exception):
    """The source needs a human in a browser; ``url`` is the page to open."""

    def __init__(self, url: str, message: str | None = None) -> None:
        super().__init__(
            message
            or "Browser verification required. Open the source, then enter its "
            "final file URL."
        )
        self.url = url


@dataclass(frozen=True)
class ParsedURL:
    url: str
    host: str
    origin: str
    path: str


def parse_url(value: str) -> ParsedURL:
    if (
        not isinstance(value, str)
        or len(value) > 8192
        or re.search(r"[\s\\\x00-\x1f]", value)
    ):
        raise ResolveError("Invalid HTTP URL.")
    m = _URL_RE.match(value)
    if not m or not m.group(2) or m.group(2).startswith("."):
        raise ResolveError("Invalid HTTP URL.")
    scheme, host, port, path = m.group(1), m.group(2).lower(), m.group(3), m.group(4)
    return ParsedURL(
        url=value,
        host=host,
        origin=f"{scheme.lower()}://{host}{port or ''}",
        path=path or "/",
    )


def _is_host(host: str, domain: str) -> bool:
    return host == domain or host.endswith(f".{domain}")


def source_kind(url: str, requires_browser: bool = False) -> str:
    """Classify a URL's flow, mirroring overdrive ``sourceKind``."""
    p = parse_url(url)
    provider = next(
        (name for domain, name in _HOST_FLOWS.items() if _is_host(p.host, domain)),
        None,
    )
    if provider == "mediafire" and p.host.startswith("download"):
        return "direct"
    if provider == "vikingfile" and re.match(r"^/d/", p.path):
        return "direct"
    if provider:
        return provider
    if any(_is_host(p.host, d) for d in BROWSER_HOSTS):
        return "browser"
    if (
        (p.host == "archive.org" and p.path.startswith("/download/"))
        or (p.host == "github.com" and "/releases/download/" in p.path)
        or _DIRECT_EXT_RE.search(p.path)
    ):
        return "direct"
    if requires_browser:
        return "browser"
    return "direct"


def _decode_entities(value: str) -> str:
    return (
        value.replace("&amp;", "&").replace("&quot;", '"').replace("&#39;", "'")
    )


def _absolute(value: str, base: str) -> str:
    value = _decode_entities(value)
    bp = parse_url(base)
    if value.startswith("//"):
        value = f"{bp.origin.split(':')[0]}:{value}"
    elif value.startswith("/"):
        value = bp.origin + value
    elif not re.match(r"^https?:", value, re.I):
        value = re.sub(r"[^/]*$", "", base.split("?")[0].split("#")[0]) + value
    parse_url(value)
    if base.startswith("https:") and not value.startswith("https:"):
        raise ResolveError("Provider attempted an insecure redirect.")
    return value


def _anchor_href(html: str, anchor_id: str) -> str | None:
    for tag in re.findall(r"<a\b[^>]*>", html, re.I):
        m = re.search(r"""(?:^|\s)id\s*=\s*["']([^"']*)["']""", tag, re.I)
        if m and m.group(1) == anchor_id:
            h = re.search(r"""(?:^|\s)href\s*=\s*["']([^"']*)["']""", tag, re.I)
            return h.group(1) if h else None
    return None


def _is_html(response: httpx.Response) -> bool:
    return bool(
        re.search(
            r"(?:text/html|application/xhtml)",
            response.headers.get("content-type", ""),
            re.I,
        )
    )


@dataclass(frozen=True)
class Resolved:
    url: str
    expected_bytes: int | None = None


def _archive_refusal(url: str, status_code: int) -> bool:
    """An archive.org 401/403 is a dead item, not a gate a browser can pass.

    A restricted or taken-down archive.org item answers 401/403; no login the
    user can do in a browser grants the server access, so it is treated as a
    dead source (skip to the next) rather than a captcha to solve.
    """
    try:
        host = parse_url(url).host
    except ResolveError:
        return False
    return _is_host(host, "archive.org") and status_code in (401, 403)


async def _request(
    client: httpx.AsyncClient, url: str, **kwargs
) -> httpx.Response:
    parse_url(url)
    resp = await client.request(url=url, **kwargs)
    if resp.status_code in (404, 410) or _archive_refusal(url, resp.status_code):
        raise ResolveError(
            f"Source unavailable: HTTP {resp.status_code}. Choose another source."
        )
    if resp.status_code in (401, 403, 429):
        raise BrowserRequired(url)
    if not resp.is_success and not (300 <= resp.status_code < 400):
        raise ResolveError(f"Provider HTTP {resp.status_code}.")
    return resp


async def _direct(client: httpx.AsyncClient, url: str) -> Resolved:
    parse_url(url)
    resp = await _request(client, url, method="HEAD", follow_redirects=True)
    if _is_html(resp):
        raise BrowserRequired(url)
    return Resolved(url=str(resp.url) or url)


async def _filekeeper(client: httpx.AsyncClient, url: str) -> Resolved:
    code_m = re.match(r"^/([a-z0-9]{12})(?:[/?]|$)", parse_url(url).path, re.I)
    if not code_m:
        raise BrowserRequired(url)
    code = code_m.group(1)
    await asyncio.sleep(_FILEKEEPER_COUNTDOWN_S)
    nxt = await _request(
        client,
        url,
        method="POST",
        follow_redirects=False,
        headers={
            "Content-Type": "application/x-www-form-urlencoded",
            "Referer": url,
        },
        content=f"op=download2&id={code}&rand=&referer=&method_free=&down_direct=1",
    )
    location = nxt.headers.get("location")
    if not location:
        raise ResolveError("Source unavailable: FileKeeper gave no file.")
    file_url = _absolute(location, url)
    head = await _request(client, file_url, method="HEAD", follow_redirects=True)
    if _is_html(head) or head.status_code >= 300:
        raise BrowserRequired(url)
    try:
        bytes_ = int(head.headers.get("content-length", ""))
    except ValueError:
        bytes_ = None
    return Resolved(url=str(head.url) or file_url, expected_bytes=bytes_ or None)


async def resolve(
    client: httpx.AsyncClient,
    url: str,
    *,
    direct_url: str | None = None,
    requires_browser: bool = False,
) -> Resolved:
    """Resolve ``url`` to a directly-downloadable file URL.

    ``direct_url`` is a link a user captured in a browser; it is trusted to be a
    file and only HEAD-verified. Raises ``BrowserRequired`` when a human step is
    needed, or ``ResolveError`` when the source is dead.
    """
    if direct_url:
        return await _direct(client, direct_url)
    kind = source_kind(url, requires_browser)
    if kind == "direct":
        return Resolved(url=parse_url(url).url)
    if kind in BROWSER_FLOWS:
        raise BrowserRequired(url)
    if kind == "filekeeper":
        return await _filekeeper(client, url)
    resp = await _request(client, url, method="GET", follow_redirects=True)
    base = str(resp.url) or url
    if "attachment" in resp.headers.get("content-disposition", ""):
        return await _direct(client, base)
    html = resp.text
    candidate = None
    if kind == "mediafire":
        candidate = _anchor_href(html, "downloadButton")
    elif kind == "vikingfile":
        candidate = _anchor_href(html, "download-link")
    if not candidate:
        raise BrowserRequired(url)
    return await _direct(client, _absolute(candidate, base))
