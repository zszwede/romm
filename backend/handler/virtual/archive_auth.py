"""Authenticated archive.org access for virtual-ROM sync and download.

Access-restricted archive.org items (e.g. the Redump CHD collections) answer 401
to anonymous requests. When IA S3 keys are configured, the account's download
entitlements are used via the S3 ``LOW`` authorization header.

The wrinkle: archive.org 302-redirects a ``download/<id>/<file>`` URL to a CDN
node on a *different* ``*.archive.org`` host, and HTTP clients (httpx included)
strip the ``Authorization`` header on a cross-host redirect — which would hand
the CDN an unauthenticated request and earn a 401. So requests to archive.org
are followed manually here, re-attaching the auth header on every hop that stays
within ``archive.org`` and dropping it the moment a redirect leaves it, so the
key is never sent to another host. Non-archive URLs are followed normally with
no auth.
"""

from __future__ import annotations

from urllib.parse import urlsplit

import httpx2 as httpx

from config import IA_S3_ACCESS_KEY, IA_S3_SECRET_KEY

_MAX_REDIRECTS = 6
# httpx uses its own sentinel for "no override"; mirror the default timeout when
# the caller doesn't pass one.
_USE_CLIENT_DEFAULT = httpx.USE_CLIENT_DEFAULT


def has_credentials() -> bool:
    """Whether archive.org downloads will be authenticated."""
    return bool(IA_S3_ACCESS_KEY and IA_S3_SECRET_KEY)


def is_archive_host(url: str) -> bool:
    host = (urlsplit(url).hostname or "").lower()
    return host == "archive.org" or host.endswith(".archive.org")


def _auth_headers(url: str) -> dict[str, str]:
    """The S3 ``LOW`` header for an archive.org URL, when keys are set."""
    if is_archive_host(url) and IA_S3_ACCESS_KEY and IA_S3_SECRET_KEY:
        return {"authorization": f"LOW {IA_S3_ACCESS_KEY}:{IA_S3_SECRET_KEY}"}
    return {}


async def send_following(
    client: httpx.AsyncClient,
    method: str,
    url: str,
    *,
    headers: dict[str, str] | None = None,
    stream: bool = False,
    timeout=_USE_CLIENT_DEFAULT,
) -> httpx.Response:
    """Issue a request and follow redirects manually, carrying archive.org auth
    across the cross-host hop to a CDN node.

    With ``stream=True`` the returned response is open and unread — the caller
    must iterate it and ``await response.aclose()``. Raises on too many
    redirects; transport/HTTP errors propagate as usual.
    """
    extra = dict(headers or {})
    current = url
    for _ in range(_MAX_REDIRECTS + 1):
        request = client.build_request(
            method,
            current,
            headers={**extra, **_auth_headers(current)},
            timeout=timeout,
        )
        response = await client.send(request, stream=stream, follow_redirects=False)
        if response.is_redirect:
            location = response.headers.get("location")
            # Drain/close before re-issuing so the connection is released.
            if stream:
                await response.aclose()
            else:
                await response.aread()
                await response.aclose()
            if not location:
                return response
            current = str(httpx.URL(current).join(location))
            continue
        return response
    raise RuntimeError(f"Exceeded {_MAX_REDIRECTS} redirects fetching {url}")
