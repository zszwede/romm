# Virtual ROMs (archive.org)

Virtual ROMs are file-less ROM entries sourced from an archive.org index. They
show in a platform's game grid with full metadata and media, but have no file on
disk until a user downloads one — at which point the entry becomes a normal ROM.

This is built on the existing physical-game machinery (`is_physical`): a virtual
ROM carries the same metadata as a digital one, is excluded from
`has_file_on_disk`, and is never flagged missing or cleaned up while virtual.

## Concepts

- **Source** (`virtual_rom_sources` table): an archive.org URL assigned to a
  platform, with an optional region allow-list. Syncing a source creates one
  virtual ROM per game it lists.
- **Virtual ROM**: a `Rom` with `is_virtual=True` and a `source_url`, living in a
  `.virtual` sentinel folder. No `RomFile`, size 0 (or the archive's size).
- **Materialize**: downloading the `source_url` into the real platform folder,
  hashing it, creating a `RomFile` and clearing `is_virtual`.

## Accepted source URLs

All normalised to `(identifier, archive_path?)` (see
`backend/handler/virtual/archive_org.py`):

- `https://archive.org/details/<id>`
- `https://archive.org/download/<id>` (± trailing slash)
- `https://iaNNN.us.archive.org/NN/items/<id>/` (CDN-node URL)
- `https://archive.org/download/<id>/<file>` (a single file)
- `https://archive.org/download/<id>/<zip>/` (a zip's contents)
- `https://iaNNN.us.archive.org/view_archive.php?archive=/NN/items/<id>/<zip>`

An **item** lists its top-level ROM/archive files via the metadata API; a lone
large archive at the root is auto-detected and its members enumerated instead.
An **archive** lists its members via `view_archive.php`, each fetched on demand
from `download/<id>/<zip>/<member>` (archive.org extracts a single member, so a
game is pulled without downloading the whole set).

## API

- `POST /api/virtual/assign` — `{urls[], platform_id?|fs_slug?, region_filter?, sync?}`.
  Creates the platform (and its `roms/`, `downloaded_media/`, `gamelists/`
  folders) when it does not exist yet, registers the sources, and starts a sync.
- `GET /api/virtual/sources?platform_id=` — list sources.
- `DELETE /api/virtual/sources/{id}` — remove a source (its ROMs stay).
- `POST /api/virtual/platforms/{id}/sync` — re-read sources, add new ROMs.
- `POST /api/virtual/roms/{id}/download` — materialize one ROM.

Sync and download run as background tasks (a set can list thousands of games; a
download can be gigabytes).

## Deployment

`VIRTUAL_DOWNLOAD_BASE_PATH` (env) is the writable root for folder creation and
downloads. It defaults to the library, which suits a writable library. When the
library is read-only — e.g. the ES-DE overlay in this deploy — point it at a
writable mount of the real `roms`/`downloaded_media`/`gamelists` tree:

```yaml
environment:
  - VIRTUAL_DOWNLOAD_BASE_PATH=/esde-rw
volumes:
  - ${ESDE_ROOT}:/esde-rw:rw   # only the virtual-ROM handler writes here
```

This is the single writable window into the otherwise read-only ES-DE library.

**Overlay caveat:** when the library ROM folder is a read-only overlay whose
lowerdir is the real `roms` tree, a just-downloaded file appears through the
overlay on the next lookup; if it does not show immediately, a quick rescan of
the platform (or `podman restart romm`) makes it visible.
