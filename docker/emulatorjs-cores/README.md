# EmulatorJS cores copied over the ones in the EmulatorJS release

Built with `EmulatorJS/build` (`build.sh --core=<name>`, Emscripten 3.1.74) and
repacked so the EmulatorJS 4.2.3 player in RomM 5.4.0 accepts them:
`build.json` inside each `.data` archive has `minimumEJSVersion` lowered to
4.2.2. `docker/Dockerfile` copies each file into
`/var/www/html/assets/emulatorjs/data/cores/`.

| Core | Platform | Source | Changes |
|---|---|---|---|
| `puae` | amiga, amiga-cd32, commodore-cdtv | `EmulatorJS/libretro-uae` @ `063af58` | none; the shipped build crashes on every WHDLoad `.lha` (EmulatorJS #1240) |
| `atari800` | atari800 | `libretro/libretro-atari800` @ `4e7fbc7` | `patches/atari800-memstream-names.patch` |
| `hatari2014` | atari-st | `libretro/hatari`, branch `hitari2014-mercurial` @ `ab55c3e` | `patches/hatari2014-little-endian.patch` (the makefile has CRLF line endings: `git apply --ignore-whitespace`), `patches/retroarch-hatari2014-async.patch` |

Frontend for all three: `EmulatorJS/RetroArch` @ `1eb5edf`.

## Why each change is needed

- **atari800**: the core carries its own `memstream_*` functions, and the
  statically linked frontend has older ones with the same names and different
  signatures. The linker warns ("function signature mismatch") and the core
  traps at startup. The patch renames the core's copies.
- **hatari2014, byte order**: the core's SDL shim headers assume big-endian
  unless `LSB_FIRST` is defined, and the Emscripten target did not define it.
  Every TOS ROM was then rejected as invalid.
- **hatari2014, coroutines**: the core uses libco, which needs the frontend
  linked with full asyncify. The link script enables that only for a fixed list
  of cores; the patch adds this one.
- **`core.json` in the atari800 and hatari2014 archives**: `m3u` is removed from
  `extensions`. With it listed the player invents a `.cue` file for every game
  that has no playlist, and the core is handed that instead of the disk image.

## Rebuilding

Add the two entries at the end of `patches/cores.json` to `cores.json` in a
checkout of `EmulatorJS/build`. Run `build.sh --core=<name>` once so the
sources are cloned under `compile/` (for `hatari2014`, clone the branch into
`compile/hatari2014` by hand first: the script's shallow clone only has the
default branch), apply the patches there, and run it again. Then replace
`build.json` and, for the two Atari cores, `core.json` in each archive with `7z u`.

A platform also needs an entry in `_EJS_CORES_MAP` (`frontend/src/utils/index.ts`)
to get a Play button. Rebuild script for `puae`: github.com/zszwede/ejs-puae-fix.

The rebuilt `puae` reads Kickstarts from the content directory (`/`), not from
`userdata/system` as the 4.2.3 build did. `SYSTEM_DIRECTORY` in
`frontend/src/v2/utils/playerFirmware.ts` is set to match; restore it when
these cores are dropped.
