# Preserve Metadata-Only Events

Status: ready-for-agent

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Preserve Metadata-Only Events as an external public API while keeping upstream's combined public stream event behavior. External consumers should be able to subscribe to `/events/metadata` for current-track changes and receive last-known metadata immediately on connect. Combined `/events` clients should continue receiving stream and metadata events from one connection.

## Acceptance criteria

- [ ] `/events/metadata` exists as the canonical metadata-only SSE endpoint for external consumers.
- [ ] `/events/metadata` replays the last known metadata for visible mounts when a client connects.
- [ ] Metadata-only events are filtered so unlisted or invisible mounts are not exposed publicly.
- [ ] `/events` continues to emit upstream public stream/video fields and also emits metadata events for combined public clients.
- [ ] Transcoded outputs preserve upstream's broader metadata mirroring while still triggering fork metadata notification semantics where required.
- [ ] AutoDJ Gap Filler behavior is respected: generated silence emits no metadata and real track metadata arrives after the first real track frame is active.

## Blocked by

- `docs/features/upstream-retention-integration/issues/01-upstream-first-integration-baseline.md`
- `docs/features/upstream-retention-integration/issues/05-port-autodj-gap-filler.md`

