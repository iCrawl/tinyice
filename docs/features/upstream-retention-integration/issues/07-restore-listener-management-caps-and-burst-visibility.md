# Restore Listener Management, Caps, And Burst Visibility

Status: ready-for-agent

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Restore live listener management, per-mount listener caps, and per-mount burst visibility on top of upstream listener handling. Operators should be able to inspect active listeners, disconnect or move supported listeners, enforce per-mount capacity limits, and see configured/effective burst values while the integrated branch uses upstream's larger default listener burst.

## Acceptance criteria

- [ ] Admin/API surfaces list active HTTP and WebRTC playback listeners with protocol, current mount, requested mount, remote address, user agent, connection time, and selectable IDs.
- [ ] Operators can disconnect individual listeners.
- [ ] Operators can move supported HTTP listeners between mounts, and unsupported WebRTC moves return a clear conflict or unsupported response.
- [ ] Per-mount listener caps are enforced in addition to global listener limits.
- [ ] Per-mount listener caps persist through stream create/update flows.
- [ ] Stream APIs and admin UI expose configured `burst_size` and effective burst size.
- [ ] The integrated branch uses upstream's larger default listener burst when no per-mount override is configured.
- [ ] Listener registry hooks do not remove upstream listener write deadlines, stream fanout fixes, or HLS/WHEP viewer counting.

## Blocked by

- `docs/features/upstream-retention-integration/issues/01-upstream-first-integration-baseline.md`

