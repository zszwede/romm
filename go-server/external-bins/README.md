# external-bins

RomM's bundled external tool binaries for the in-process downloader. Point the
server at this folder with `ROMM_EXTERNAL_BINS` (default: an `external-bins/`
dir beside the romm-go executable).

Expected contents:

- `switch-tool` — the Nintendo Switch decompress/merge tool. Build it from the
  switch-tools source and copy the release binary here:
  `cp .../switch-tools/target/release/switch-tool ./switch-tool`

The actual binaries are host/arch-specific and are not committed (see
`.gitignore`); only this README is tracked.
