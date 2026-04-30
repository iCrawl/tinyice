# AutoDJ Ogg Metadata Propagation To MP3 Transcodes

## Problem Statement

TinyIce can already determine now-playing metadata for AutoDJ playback from MP3 files on disk, but that metadata does not reliably reach MP3 transcoder outputs when the source mount is an AutoDJ-produced Ogg stream.

From the operator's perspective, this creates an inconsistent listener experience:

- the AutoDJ mount knows what song is playing
- the web UI can show metadata through TinyIce state and SSE
- third-party radio players listening to a transcoded MP3 mount do not receive useful ICY metadata, even when they already negotiate ICY correctly

The operator does not want to split the listener base further by introducing more public listener URLs just to get metadata working. The primary web player path must remain safe for plain browser audio playback. The desired outcome is that MP3 transcodes derived from an AutoDJ Ogg source behave like a normal radio stream for ICY-capable clients without changing the default safety of the existing listener contract.

## Solution

Use the existing AutoDJ metadata as the source of truth for the source mount, then mirror that metadata into transcoded MP3 output mounts so those mounts expose the correct current song to the normal ICY emission path.

The solution keeps the current listener contract intact:

- Ogg listener mounts remain pass-through audio streams
- MP3 listener mounts only emit ICY metadata when a client requests it
- plain browser audio playback remains unchanged
- no new public listener URLs are required for the main use case

The implementation focus is metadata propagation rather than protocol expansion:

1. Treat AutoDJ-produced source mounts as the authoritative source of current-song state.
2. When a transcoder starts, immediately copy the source mount's current song into the output mount.
3. While the transcoder is running, mirror future metadata changes from the input mount to the output mount.
4. Let the existing HTTP listener path emit ICY metadata for the transcoded MP3 output only when the player requests it.

This gives VLC, mpv, ExoPlayer-class clients, and other ICY-capable players the correct `StreamTitle` on transcoded MP3 mounts while keeping the browser and generic HTTP path stable.

## User Stories

1. As a station operator, I want an AutoDJ-produced Ogg mount to carry correct now-playing state inside TinyIce, so that derived outputs can reuse it.
2. As a station operator, I want MP3 transcodes of an Ogg AutoDJ mount to inherit the same current song, so that every format represents the same program.
3. As a station operator, I want the transcoded MP3 mount to expose correct ICY metadata to compatible players, so that listeners see the track name in their player.
4. As a station operator, I want the main public mount behavior to stay stable for browser playback, so that the TinyIce web player does not regress.
5. As a station operator, I want to avoid introducing extra public stream URLs for the same program, so that listener discovery and sharing stay simple.
6. As a station operator, I want metadata on the MP3 fallback or compatibility format to update on track changes, so that listeners are not stuck on stale titles.
7. As a station operator, I want metadata to appear correctly even if a transcoder starts mid-song, so that I do not have to wait for the next track boundary.
8. As a station operator, I want missing ID3 data to degrade gracefully, so that playback continues even when some files lack good tags.
9. As a station operator, I want metadata mirroring to follow the actual source mount, so that a transcoder output does not invent or drift from the program it represents.
10. As a station operator, I want no manual per-track metadata entry for AutoDJ files, so that the system stays automatic.
11. As a listener using VLC, I want the MP3 stream to show the current song automatically, so that the player behaves like a normal internet radio stream.
12. As a listener using mpv or FFmpeg-family players, I want the MP3 stream to expose the current title when ICY is negotiated, so that the player UI reflects the song that is actually playing.
13. As a listener using ExoPlayer or a Media3-based Android app, I want the MP3 stream to expose current-song metadata through normal ICY mechanisms, so that mobile apps can display the correct track.
14. As a listener using a browser-based TinyIce player, I want playback to remain safe and uninterrupted, so that metadata improvements for radio clients do not break the web experience.
15. As a developer, I want transcoder metadata behavior to be deterministic, so that it can be tested without depending on fragile timing or implementation details.
16. As a developer, I want startup metadata backfill to be explicit, so that transcoder outputs are correct immediately after startup.
17. As a developer, I want future metadata updates to be mirrored through a single runtime responsibility, so that the logic is not duplicated across listener handlers and encoders.
18. As a developer, I want transcoder restart behavior to clean up metadata subscriptions, so that retries do not leak goroutines or duplicate updates.
19. As a developer, I want tests to prove that ICY output stays opt-in, so that metadata improvements do not silently change the browser-safe listener contract.
20. As a developer, I want metadata mirroring to be limited to song state and not broader stream identity, so that output mounts keep their own format-specific naming and bitrate characteristics.
21. As a support engineer, I want API and admin views of the transcoded mount to show the same current song as listeners see in compatible players, so that debugging remains straightforward.
22. As a station operator, I want the implementation to focus first on AutoDJ-produced Ogg mounts, so that the main use case is solved without taking on unrelated external Ogg ingest complexity.
23. As a station operator, I want external live Ogg sources to remain unaffected in the first phase, so that this change does not widen the rollout surface unnecessarily.
24. As a developer, I want the metadata propagation logic to be reusable for future derived outputs, so that later formats can benefit from the same source-of-truth model.

## Implementation Decisions

- AutoDJ-produced source mounts are the primary source of truth for this feature.
  - The first implementation slice targets source mounts generated by TinyIce AutoDJ from MP3 files on disk.
  - Metadata embedded in those MP3 files is sufficient when present.
  - Missing or partial file metadata is accepted as a normal degraded case.

- MP3 transcoder outputs always mirror song metadata from their input mount.
  - The transcoder output represents the same program in another format.
  - Song metadata should therefore stay aligned with the input mount rather than being managed independently.

- Metadata mirroring needs a dedicated runtime responsibility.
  - The transcoder path should gain a small, deep module responsible for metadata backfill, live mirroring, and cleanup.
  - That module should expose a simple lifecycle surface: initialize from input state, subscribe to future changes, and stop cleanly.
  - The goal is to keep metadata propagation separate from codec-specific transcode loops so the encoder path stays focused on audio transport.

- Metadata backfill on transcoder startup is required.
  - When a transcoder starts in the middle of a song, the output mount should immediately receive the source mount's current song.
  - Waiting for the next track change is not acceptable.

- The existing HTTP ICY contract remains unchanged.
  - MP3 listener mounts continue to emit ICY metadata only when the client negotiates it.
  - No forced ICY insertion is added to the default listener path.
  - Browser-safe playback remains a hard requirement.

- Ogg listener mounts are not rewritten inline in this phase.
  - The implementation does not attempt dynamic packet rewriting or live comment injection inside Ogg listener streams.
  - The requirement is satisfied if TinyIce state is correct and MP3 transcodes expose the metadata through existing ICY behavior.

- Stream identity and song state remain distinct concerns.
  - The output mount should inherit current-song metadata from the input mount.
  - The output mount should keep its own output identity, format, bitrate, and related output-specific fields.

- No new public API or configuration surface is required for phase 1.
  - Existing admin, listener, and API flows should gain the improved behavior without requiring a new toggle.
  - If future phases need operator control, they can build on the same internal metadata propagation model.

- External live Ogg ingest is deferred.
  - This PRD does not require solving generic metadata extraction from arbitrary external Ogg sources.
  - A future phase may extend the same metadata propagation approach to external ingest once the AutoDJ path is complete.

## Testing Decisions

- Good tests should validate externally observable behavior rather than implementation details.
  - Assert the current song visible on source and output mounts.
  - Assert the presence or absence of ICY metadata from listener responses based on negotiation behavior.
  - Avoid coupling tests to internal goroutine structure, subscription container shape, or exact helper function boundaries.

- The metadata propagation module should be tested directly.
  - Verify startup backfill from source mount to output mount.
  - Verify live mirroring of later metadata changes.
  - Verify clean shutdown and restart behavior without duplicate propagation.

- The transcoder runtime should be covered with integration-style tests.
  - Validate that a running transcoder output inherits current-song state from the input mount.
  - Validate that a later input metadata change updates the output mount.

- The HTTP listener path should be tested at the behavior level.
  - Verify that a transcoded MP3 mount emits `icy-metaint` and an ICY metadata block only when the listener requests ICY metadata.
  - Verify that the same mount does not emit ICY metadata for plain listeners.

- AutoDJ metadata timing should continue to be protected.
  - Prior tests already establish that AutoDJ metadata is published only after the track becomes active.
  - The new work should compose with that timing rather than weakening it.

- Prior art in the codebase already covers the needed shapes.
  - There are existing tests for continuous transcoder output behavior.
  - There are existing tests for AutoDJ metadata emission timing.
  - There are existing regression tests around metadata events and listener-facing stream behavior.
  - The new tests should follow those patterns while focusing on source-to-output metadata propagation.

## Out of Scope

- Dynamic inline metadata injection or packet rewriting for Ogg listener streams.
- Generic metadata extraction for arbitrary external live Ogg sources.
- Changing browser playback to consume ICY metadata directly.
- Forcing ICY metadata on listeners that do not request it.
- Adding new public stream URLs, playlists, or compatibility endpoints for this phase.
- Broad metadata normalization beyond the current-song value needed for now-playing behavior.
- Reworking the overall transcoder architecture beyond the metadata propagation responsibility required for this feature.

## Further Notes

- This PRD intentionally solves the narrow, high-value path first: AutoDJ-produced Ogg source mounts that feed MP3 transcoder outputs.
- The feature should preserve the existing principle that TinyIce stays safe for browser playback while still serving metadata-rich radio clients that negotiate ICY correctly.
- If later work is needed for external Ogg ingest, the preferred extension path is to feed that metadata into the same internal source-of-truth and propagation model rather than creating a separate downstream metadata system.
