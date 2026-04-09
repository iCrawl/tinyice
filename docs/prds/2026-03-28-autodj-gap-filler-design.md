# AutoDJ Transition Gap Filler — Design Spec

## Goal

Eliminate listener-facing stream open failures during AutoDJ song transitions by keeping each AutoDJ mount's encoded output continuous across track boundaries.

The motivating symptom is a fresh listener connection that receives `200 OK` but only a tiny initial payload during the gap between songs, causing Chromium to fail stream initialization with `PipelineStatus::DEMUXER_ERROR_COULD_NOT_OPEN`.

This design targets AutoDJ-managed mounts only. It does not change manual source ingest, relays, WebRTC ingest, or generic source-stall handling.

## Problem Summary

Today AutoDJ reuses the same relay `Stream` object for a mount, but it does not maintain a continuous encoder session across tracks.

Current behavior:

1. AutoDJ selects one file.
2. The file is decoded to PCM.
3. The PCM is encoded to the configured output format (`mp3` or `opus`).
4. When the file ends, that encode run exits.
5. AutoDJ selects and prepares the next file.
6. A new encode run starts for the next file.

The relay mount remains the same, but there is a handoff window where a brand-new listener can connect and receive an HTTP response before enough valid decodable media is available. Existing listeners are usually tolerant because they are already inside an established decoder session. New listeners are not.

## Non-Goals

- No frontend-only workaround as the primary fix.
- No synthetic silence for manual `Play` / `Pause` or `Stop`.
- No change to non-AutoDJ sources.
- No pre-encoded silence asset library per format / bitrate.
- No change to the public mount or listener API shape.

## Chosen Approach

Introduce a persistent AutoDJ output session per mount that owns the encoder lifetime. That session writes to the existing relay `Stream` continuously for as long as the AutoDJ is in the playing state.

Instead of treating each song as its own independent encoder run, songs become PCM inputs that are fed into one long-lived output session:

- real track PCM when a song is available
- generated silence PCM when AutoDJ is between songs

This keeps the encoded stream alive across handoffs while preserving the existing mount, listener, and transcoder topology.

## Rejected Alternatives

### 1. Frontend-only reconnect avoidance

This would help one player implementation, but the bug can affect any consumer of `/stream` or a transcoded fallback mount. The failure mode is server-output continuity, not just frontend control behavior.

### 2. Pre-encoded silence blobs

This would require maintaining encoded silence for multiple formats, bitrates, and possibly sample rates. It is brittle for Ogg/Opus continuity and unnecessary because the server already has decode-to-PCM and encode pipelines.

### 3. Small per-transition silence bursts with today's per-track encoder model

This is simpler, but it preserves the structural discontinuity between tracks. It reduces the gap but does not actually turn the AutoDJ output into one continuous encoded session.

## Output Model

Each AutoDJ mount gets a persistent `AutoDJOutputSession` conceptually responsible for:

- owning the relay output `Stream`
- owning the long-lived encoder loop
- accepting PCM input from either a track source or a silence source
- pacing encoded output continuously
- switching input sources atomically without tearing down the stream

The existing relay `Stream` identity remains unchanged. The change is at the producer layer feeding it.

## Fixed Internal PCM Format

A long-lived encoder needs a stable PCM input format. The session therefore normalizes all source audio into a fixed internal PCM bus per output format:

| Output format | Internal PCM sample rate | Channels |
|---------------|--------------------------|----------|
| `mp3`         | 44100 Hz                 | 2        |
| `opus`        | 48000 Hz                 | 2        |

Why:

- Opus already expects a fixed 48 kHz encode path.
- MP3 output is simpler and more predictable if the encoder runs at one stable sample rate instead of changing with each song.
- Silence generation becomes trivial: zeroed PCM frames in the normalized session format.
- Transcoded outputs benefit automatically because the source mount never goes dry.

## Data Flow

### Current file path

`MP3 file -> mp3 decoder -> per-track encoder -> relay Stream`

### New file path

`MP3 file -> mp3 decoder -> PCM normalizer -> AutoDJ output session -> long-lived encoder -> relay Stream`

### Gap path

`silence generator -> AutoDJ output session -> same long-lived encoder -> same relay Stream`

The output session decides which PCM source is active at a given moment. The encoder does not stop when the source changes from track to silence or silence to track.

## PCM Sources

Define a small internal abstraction for PCM frame producers. The implementation does not need a large interface hierarchy, but it should separate the responsibilities clearly:

- `TrackPCMSource`: reads from the selected song's decoder and normalizes to the session PCM format
- `SilencePCMSource`: yields zeroed PCM frames at the session PCM format and frame cadence

The output session reads from the currently active PCM source in fixed frame units appropriate for the target encoder.

## Session Lifecycle

### Play

When AutoDJ enters `StatePlaying`:

1. Create or reuse the relay `Stream` for the mount.
2. Create the persistent output session for the mount if it does not exist.
3. Start the encoder loop once.
4. Select the first track.
5. Feed track PCM into the session.

### Automatic track transition

When the current track reaches EOF:

1. Switch the session input to silence immediately.
2. Resolve and prepare the next playable track.
3. Create the next track PCM source and normalize it to the session format.
4. Atomically swap the session input from silence to the next track.
5. Update track metadata only when the real track becomes active.

### Stop / Pause

When AutoDJ stops through existing controls:

1. Stop the output session.
2. Stop the encoder loop.
3. Do not emit silence indefinitely for a stopped mount.

Silence filler exists only for automatic between-song handoff while AutoDJ remains in playing state.

## Metadata Rules

Silence is transport filler, not a track.

Therefore:

- Do not publish metadata changes for silence.
- Keep the previous metadata visible until the next real track is ready.
- Publish the next metadata exactly when the new real track becomes the active PCM source.

This keeps listener metadata stable and avoids fake song history entries or misleading SSE updates.

## Queue / Playlist Semantics

Track selection semantics remain unchanged:

- queue still has priority over playlist
- external song command still overrides playlist when configured
- loop and shuffle behavior remain unchanged
- invalid files are still skipped

The only behavioral change is what the output session does while the next real track is being prepared.

## Error Handling

### Next track preparation failure

If a selected next track fails validation or decoding:

- keep the session in silence mode
- log the failure with mount and file path
- continue selection logic until a playable track is found or the playlist is exhausted

### No next track available

If the AutoDJ has no queue item, no playlist entry, and no successful song-command result:

- exit playing state using the current stop semantics
- stop the output session rather than emitting silence forever

### Encoder failure

If the long-lived encoder loop fails:

- mark the session as failed
- log the failure prominently
- stop the AutoDJ output session
- allow existing streamer supervision behavior to restart playback through normal control flow instead of silently looping in a broken state

## Transcoder Interaction

No transcoder-specific silence feature is needed.

Transcoders subscribe to source mounts. Once an AutoDJ source mount becomes continuous across transitions, a transcoder reading that mount inherits the continuity automatically.

That means:

- `/stream` handoff continuity improves directly
- `/fallback` continuity improves when it is transcoded from `/stream`

## Testing

### Unit tests

Add tests for:

- switching from track PCM to silence PCM without stopping the session
- switching from silence PCM to next-track PCM without reinitializing the session
- no metadata emission while silence is active
- metadata emission when the next real track activates
- stop semantics when no next song exists

### Integration tests

Add AutoDJ-focused integration coverage for:

- a listener connecting during a forced track handoff still receiving a decodable stream
- a transcoder output remaining available while the source AutoDJ transitions tracks
- queue / playlist / song-command path still selecting the same next song as before

### Regression instrumentation

Add debug logging around:

- end of current track
- silence mode enter
- next track ready
- silence mode exit
- gap duration in milliseconds

This makes it possible to confirm the feature is actually covering the handoff window instead of only changing symptoms.

## Implementation Notes

The design intentionally prefers one continuous output session over a smaller patch because the bug is fundamentally about encoder continuity, not only about inserting more bytes.

Expected refactor areas:

- `relay/streamer.go`
  - move from per-track encode calls to a mount-level output session
  - keep track-selection logic, but change how selected tracks are fed downstream
- `relay/transcode.go`
  - extract reusable encoder-session helpers from the current single-reader encode functions
- `relay/pcm_resample.go`
  - reuse the existing frame normalization logic for both track PCM and silence PCM paths

The refactor should preserve existing listener-facing mount URLs, metadata APIs, and AutoDJ controls.

## Rollout Scope

Phase 1 for this work is AutoDJ only.

If the design works well, the same session-based continuity model can later be generalized to other ingest paths that currently expose short source gaps.
