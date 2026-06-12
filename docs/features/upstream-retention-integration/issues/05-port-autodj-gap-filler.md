# Port AutoDJ Gap Filler

Status: ready-for-agent

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Port AutoDJ Gap Filler onto upstream's current decoder, encoder, and stream session internals. AutoDJ mounts should remain continuously decodable during normal between-track transitions by emitting generated silent PCM, while real metadata is published only when a real track becomes active.

## Acceptance criteria

- [ ] A listener joining an AutoDJ mount during a normal between-track transition receives a decodable stream rather than an empty or tiny undecodable response.
- [ ] Generated silence is used only for normal AutoDJ between-track gaps.
- [ ] Generated silence does not publish metadata.
- [ ] Track metadata is published only after the first real track frame has become active.
- [ ] Manual stop, pause, delete, and shutdown are not masked by endless generated silence.
- [ ] Decoder shutdown and recovery workers unblock and exit cleanly.
- [ ] The implementation uses upstream stream session, Ogg/header, decoder, and encoder behavior instead of restoring old fork transcode or stream internals wholesale.

## Blocked by

- `docs/features/upstream-retention-integration/issues/01-upstream-first-integration-baseline.md`
- `docs/features/upstream-retention-integration/issues/03-port-mount-ownership-registry.md`

