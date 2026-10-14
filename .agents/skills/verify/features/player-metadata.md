# Player and metadata

## Sub-features

Listen on the public Player, display current track metadata, replay current metadata on connection, and clear ended playback. Preserve video and transport fields.

## How to get to it (user POV)

Open `/player/verify-radio` after smoke creates the visible AutoDJ. Public listing is `/explore`. WebRTC playback is `/player-webrtc/verify-radio`. Inspect the browser snapshot for the current play control before clicking.

## Driving it with HTTP and T3 preview

Capture Player before playback, click its actual play control, and record audio delivery plus the displayed title. The helper's `audio-prefix.mp3` proves baseline HTTP delivery only. Observe `/events` as a real EventSource connection. Public event data must preserve upstream stats, visibility, video, and transport fields.

On the integrated build, connect to canonical `/events/metadata` and require immediate replay for the visible mount. Metadata keeps `mount`, `title`, `artist`, and `started_at`, and adds `playback_id` and `revision`. Existing station name remains the artist. Only the first successfully published real frame starts a playback. Next into the same title must allocate a different ID. Generated gap silence must not create a track event.

Pause keeps playback identity. Stop, terminal failure, exhausted selection, and source removal clear title and replay state. Reconnect to both event endpoints and require that clear. Keep Player open across later stats ticks and verify they cannot restore an old title. Compare event revisions and reject stale updates. Hide a mount and require the metadata-only endpoint to exclude it.

For transcodes, create an output through `/api/transcoders` and compare real input and output metadata. Require source playback ID and start time on the output, its own publication revision, and propagated clears. Before decoder attachment, require metadata propagation. After a dropped event, broad reconciliation must restore it within the existing five-second interval without republishing unchanged state.

## Gotchas

Baseline upstream has `/events` but lacks the retained `/events/metadata` contract. This is an expected acceptance failure until integration. A polling stats response is insufficient evidence for SSE replay. Keep the subscriber alive and capture event name, body, revision, and receipt time. Visibility policies for combined events and metadata-only events differ; follow the integration PRD rather than broadening publication.
