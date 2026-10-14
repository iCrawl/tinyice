# AutoDJ and Studio

## Sub-features

Create an AutoDJ, browse files, add and remove playlist entries, load and save a playlist, control playback, and set volume. Integration acceptance also requires bounded gap audio, playback identity, retained failures, and cancellable recovery.

## How to get to it (user POV)

Open `/admin/autodj`, then use **Studio** for the instance. The shared Studio route is `/admin/studio?mount=%2Fverify-radio`. Transport buttons use ARIA labels **Play**, **Pause**, **Previous**, **Next**, **Shuffle**, and **Repeat**. The volume control has `aria-label="Volume"`. Library buttons use `aria-label="Add <filename>"`.

## Driving it with HTTP and T3 preview

Run helper `smoke` on a fresh instance. It seeds loopable stereo WAV files and creates `/verify-radio` through `POST /api/autodj`. Open Studio and observe the tone title and playlist. Click **Pause**, verify API `state` is `2`, and observe the control change to **Play**. Click **Play**, verify `state` is `1`, and capture the resumed title. Stopped is `0`. SSE AutoDJ events are flat objects, with `song`, `state`, and `playlist_version`.

Use `/api/autodj/%2Fverify-radio/files` and `/playlist` to check `is_dir` and numeric entry `id`. Add with `POST .../playlist/add` and `{ "path": "tone-a.wav" }`. Remove with `.../playlist/remove` and `{ "id": <entry-id> }`. Select a real `.pls` library entry before **Load**. `POST .../playlist/load?file=<selected-path>` must load that selection. Save uses `.../playlist/save`. Check the resulting file in the instance's `data/playlists` directory.

`POST .../volume` with `{ "volume": 1, "unit": "percent" }` must return `volume: 0.01`. Set 80 percent afterward for audio analysis. Open `/verify-radio` to capture real MP3 bytes and decode the tone with an available decoder. Byte delivery alone does not prove pitch, bitrate, or uninterrupted gap playback.

On the integrated build, verify normal gaps last at most ten seconds and accept new MP3 and Opus listeners. Pause, startup, exhausted selection, failures, and recovery must not produce endless filler. Repeated identical titles need distinct playback IDs. Stop, delete, and shutdown cancel readers, commands, encoders, and retry work. Inspect `recovery.phase`, `attempt`, `next_retry`, and `last_error`, plus `producer_last_progress` and `availability_last_progress`. Generated audio can advance availability but cannot count as successful producer recovery. A blocked `song_command` needs an isolated local script and a bounded timeout; document that precondition before testing it.

## Gotchas

No ffmpeg is required to generate or ingest WAV. Decoder-based tone, duration, and bitrate proof needs an available decoder. The helper proves baseline controls through HTTP and leaves the instance running for browser checks. Studio uses numeric state and flat events. Do not feed it a fabricated `currentTrack` object. Invalid playlists and missing recovery controls remain failures on an integrated build.
