# Mount Runtime Rollout Notes

## Runtime Model

- `RuntimeRegistry` owns per-mount lifecycle metadata.
- `Relay` and `Stream` remain the active audio data plane.
- The runtime is intentionally audio-first.

## What This Migration Changes

- Icecast, AutoDJ, WebRTC source, relay-pull, RTMP, and SRT mounts register their ownership in `RuntimeRegistry`.
- HLS registers itself as a mount output in `RuntimeRegistry`.
- Tenant live stream counts derive from attached mounts instead of fake pipeline ownership.

## What This Migration Does Not Change

- `Stream.Broadcast`, listener fanout, and buffer internals
- public/admin SSE contracts
- the actual transport shape of existing mounts
- true multi-track or video synchronization

## Verification

Before calling the migration stable, verify:

1. Icecast source connect and disconnect
2. AutoDJ playback and stop/restart behavior
3. Relay-pull mount registration
4. WebRTC source cleanup
5. HLS register/unregister behavior
6. Tenant usage endpoints still return sensible live stream counts

## Guidance

- Prefer extending `RuntimeRegistry` over reviving `PipelineManager` for audio-first work.
- Treat pipeline files as experimental unless a new design explicitly reopens that direction.
