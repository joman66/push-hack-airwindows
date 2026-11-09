# Airwindows for Push Hack

An effect chain of up to 8 [Airwindows](https://www.airwindows.com) effects that runs on Ableton Push 3 (standalone), as a [Push Hack](https://github.com/federico-pepe/ableton-push-hack) app.

- **510 effects in 22 categories**, from [Airwindows Consolidated](https://github.com/baconpaul/airwin2rack) (Chris Johnson's Airwindows, gathered by Paul Walker).
- **Audio from Live, back to Live.** A Live track goes into the hack over Ableton Link Audio, runs through the chain, and comes back as the Link channel `Airwindows`.
- **Edit on Push.** Open it from Push Hack Home. You get the chain, a browser sorted by category, and one page of parameters for each effect, on the encoders.
- **Edit on the web.** The hack's web page shows and changes the same chain.
- The chain is saved and comes back after a restart.

Status: alpha. Tested off the device: every effect, the chain, the Push screens, and audio through the real `core/audiobus` server. **Not yet tested on a Push 3.**

## Needs

| Part | Why |
|---|---|
| Push Hack (push-manager and push-display) | Push Hack Home, the screen and the buttons |
| push-audio-loopback **0.3.0 or newer, in Link mode** | Audio in and out. Inputs need `core/v0.5.5` or newer in the service. |
| Live with **Link** and **Link Audio** on | The audio goes over Link Audio |

The effects run at 44100 Hz, the rate of Link inputs. In the Virtual Audio Interface mode of push-audio-loopback, the card can run at another rate, and the screen then says to switch to Link.

## Set up the audio

1. In Live, turn on **Link** and **Link Audio**.
2. On the web page of **Audio Loopback**, in the routing matrix, connect a Live track (or Main) to **Airwindows > Input**.
3. In Live, make an audio track. Set its input type to **PushHack** and its channel to **Airwindows**. Set its monitor to **In**.
4. Mute the source track's own output if you want to hear only the processed sound.

Until step 2 is done, the screen says `No input: route a Live track to Airwindows > Input`.

## Use it on Push

Open Push Hack Home (Shift + Help, or hold Help) and pick **AIRWIN**. Press Help to go back to Live. The full control list is in [MANUAL.md](MANUAL.md). In short:

- **Chain page:** the 8 slots, left to right, in the order the audio goes. Upper button = on/bypass. Lower button = open the effect. The lower button of the first empty slot (or the **Add** button) opens the browser.
- **Browser:** categories on the left, effects on the right, with the description of the selected effect. Turn encoder 1 for the category and the jog wheel for the effect. **ADD** puts the effect at the end of the chain and opens its page.
- **Effect page:** 8 encoders = 8 parameters. Shift = fine. Delete + touch a knob = back to the default. The upper buttons are BACK, ON/BYPASS, MOVE left/right, REPLACE, REMOVE, and the previous/next effect.
- **Volume encoder:** output gain.

A change happens right away. Nothing asks you to confirm it.

## Web page

`http://push.local:<port>/` (Push Manager links to it). It shows the meters, the gains, each effect with its parameters as sliders, and the buttons to add, replace, move, bypass and remove. Double-click a slider to reset it.

API (JSON):

| Method | Path | Body / result |
|---|---|---|
| GET | `/api/status` | `hack_version`, number of effects, chain length |
| GET | `/api/effects` | `categories`, `effects` (`name`, `category`, `what`, `nparams`) |
| GET | `/api/state` | chain, parameters, gains, audio status, `message` |
| POST | `/api/chain` | `{"op":"insert","pos":-1,"name":"Galactic"}`; ops: `insert`, `replace`, `remove`, `move` (`pos`, `to`), `bypass` (`pos`, `bypass`), `clear` |
| POST | `/api/param` | `{"pos":0,"index":1,"value":0.7}` or `{"pos":0,"index":1,"reset":true}` |
| POST | `/api/gains` | `{"in_gain":1,"out_gain":1}` (linear, 0 to 2) |

## Files on the Push

In `/data/push-hack/hacks/airwindows/`:

- `airwindows`: the binary (static: no other file needed)
- `hack.json`
- `airwindows-chain.json`: the chain (effects by name, parameter values 0..1, bypass, gains). It is kept across updates.

Log: `/data/push-hack/logs/airwindows.log`.

## CPU

The Push runs Live too, so the effects must stay light. On a desktop x86 core, one effect takes 17 µs per 128-frame block on average, which is 0.6% of the block. The heaviest effects are amp sims: Pockey takes about 18% of a block, and PointyGuitar, PunchyDeluxe and ChimeyDeluxe take 7 to 9%. The Push CPU is slower. The screen shows **DSP %**: the worst block time against the block length. Keep it well under 100%.

## Build

Needs Go 1.25, g++ and make on Linux x86-64 (the release workflow uses `ubuntu-latest`).

```bash
make -j4 lib     # compiles the Airwindows library once (about 1,000 files, a few minutes)
make             # static binary ./airwindows
make test        # every effect through a sine, the chain, the UI, the screens
make package     # airwindows.tar.gz for the catalog
make deploy PUSH=push.local
```

The binary links glibc and libstdc++ statically, so the glibc of the build machine does not matter on the Push. To look at the screens: `cd src && AW_SHOTS=/tmp/shots go test -run TestRenderViews .`

## Publish

Push a tag `vX.Y.Z` that matches `version` in `hack.json`. The workflow builds, tests, makes the release and commits `release.json`. Then add this entry to `catalog/catalog.json` in ableton-push-hack:

```json
{
  "id": "airwindows",
  "name": "Airwindows (alpha)",
  "description": "An effect chain of up to 8 Airwindows effects (510 in 22 categories). A Live track comes in over Link Audio, runs through the chain and goes back to Live. Edit it on Push (an app of Push Hack Home) or on its web page.",
  "author": "joman66",
  "homepage": "https://github.com/joman66/push-hack-airwindows",
  "github_repo": "joman66/push-hack-airwindows",
  "default_branch": "main",
  "asset_name": "airwindows.tar.gz",
  "requires": ["push-audio-loopback"]
}
```

## Licenses

This hack: MIT ([LICENSE](LICENSE)). Airwindows and Airwindows Consolidated: MIT. See [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
