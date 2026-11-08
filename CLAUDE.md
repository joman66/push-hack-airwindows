# CLAUDE.md

Guidance for Claude Code in this repository.

## What this is

A [push-hack](https://github.com/federico-pepe/ableton-push-hack) app for Ableton Push 3: an effect chain of up to 8 Airwindows effects. Audio comes in from a Live track over Link Audio (an input of this hack on push-audio-loopback's routing matrix), goes through the chain, and goes back to Live as the Link channel `Airwindows`. It is an app of Push Hack Home (`hack.json` `surface`). There is no instrument and no MIDI effect: Airwindows has none, and the user chose an effects-only chain (2026-11-08).

## Build and test

```bash
make -j4 lib   # build/libairwin.a from third_party/airwin2rack/src + dsp/awchain.cpp
make           # ./airwindows, fully static (-extldflags -static, tags netgo,osusergo)
make test      # needs make lib first: the tests link the real library
make vet
```

Static linking is deliberate: the binary then runs on the Push whatever glibc the build machine has. Braids builds in Docker for the same reason; this repo does not need Docker. Do not add a dependency on libasound or any other shared library.

Tests (`src/*_test.go`) use the real engine. `TestEveryEffectProcesses` runs all 510 effects. `TestRenderViews` writes PNGs of every screen with `AW_SHOTS=<dir>`. Run `TestConcurrentEdits` with `-race` after a change to the chain or the audio loop.

## Layout

| File | Job |
|---|---|
| `dsp/awchain.h/.cpp` | C API over the Airwindows registry, and the chain (`aw_chain`) |
| `dsp/cmrc/cmrc.hpp` | Stand-in for CMakeRC, so the vendored `AirwinRegistry.cpp` builds unchanged (no long docs) |
| `src/aw.go` | The only cgo file. `Engine` interface, `loadCatalog` |
| `src/chain.go` | `Catalog` (sorted by category, then name) and `Chain` (layout, defaults, gains, `airwindows-chain.json`) |
| `src/audio.go` | Render loop on `core/audiobus` (`OpenWithInputs`, input `Input`) |
| `src/ui.go` | The Push controls as a pure state machine: chain, browser, plugin views |
| `src/render.go` | 960x160 frames, ASCII only |
| `src/surface.go` | Focus/blur, ALSA control input from Push's Live Port, LEDs, display loop, Home status |
| `src/web.go`, `src/ui/index.html` | Web page and JSON API |
| `src/main.go` | Supervisor (restarts the child on a crash), wiring |
| `third_party/airwin2rack/` | Vendored `src/` of Airwindows Consolidated, unchanged. MIT. |

## Rules

- **The audio thread never waits for an allocation.** `chain_insert`/`chain_replace` build the effect outside the chain mutex; `chain_remove`/`chain_replace` free it outside. Only pointer moves happen under the lock. Keep it that way.
- **The render loop never stops.** With no bus, `Read` gives silence and `Write` sleeps one block (the rule in ableton-push-hack's CLAUDE.md, "Audio from hacks").
- **Effects run at 44100 Hz.** Link inputs are 44100. `AirwinConsolidatedBase::defaultSampleRate` is set before each constructor, since some effects read it there.
- The registry drops effects with no description and the `Unclassified` category (unfinished effects, such as the ConsoleX family). `aw_count` is 510 with this vendored copy.
- Screen strings are ASCII. Effect names and categories are checked in `TestCatalog`.
- Controls count only while the app has the surface (`focused`), only from the Live Port, only on channel 0 (other channels carry pad MPE; CC 74 would read as encoder 4).
- Every LED the app lights must be in `ledCCs`, or it is never turned off (`TestLEDs`).
- Chain changes from the web page and from Push both go through `Chain`. `UI.validateLocked` sends a page whose slot was removed back to the chain view.

## Update Airwindows

Replace `third_party/airwin2rack/src` with the `src/` of a newer airwin2rack checkout (drop any `*.o`), then `make clean lib test`. A saved chain refers to effects by name; a name that disappears is skipped on load with a log line.

## Device

- Install path `/data/push-hack/hacks/airwindows/`, service `push-hack-airwindows`, log `/data/push-hack/logs/airwindows.log`.
- `make deploy PUSH=push.local` (files as `ableton@`, service as `root@`).
- Not yet run on a Push 3 (2026-11-08). First things to check there: the DSP % with a few heavy effects, focus/blur with Home, and the input route on push-audio-loopback's page.
