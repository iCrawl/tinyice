# Retain Diagnostics, Dead Streams, And Recovery

Status: needs-triage

Implementation: complete. Awaiting maintainer review. See [verification](../verification.md).

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Retain fork-owned diagnostics, Retained Dead Stream policy, and dead `song_command` recovery on top of upstream's health, history, and AutoDJ hooks. Failed AutoDJ streams should remain visible to operators by default, carry diagnostic state and recent history, and recover through the fork's health-driven recovery path where appropriate.

## Acceptance criteria

- [x] Failed AutoDJ streams remain visible as Retained Dead Streams by default rather than being auto-removed after upstream's default grace period.
- [x] Stream and AutoDJ APIs expose status, status class, reason, last error, updated timestamp, and recent diagnostic history.
- [x] Diagnostics are recorded for source connects/disconnects, health degradation/death/recovery, AutoDJ command failures, empty output, invalid files, playlist exhaustion, recovery start, recovery success, and recovery failure.
- [x] Dead `song_command` recovery starts from health-dead events and cancels on stop, delete, and shutdown.
- [x] Upstream AutoDJ `on_play_command`, track-start hooks, and webhook behavior continue to work.
- [x] Diagnostics extend upstream history behavior without replacing upstream listener history, traffic totals, geo, kiosk, or dashboard history features.

## Blocked by

- `docs/features/upstream-retention-integration/issues/01-upstream-first-integration-baseline.md`
- `docs/features/upstream-retention-integration/issues/03-port-mount-ownership-registry.md`

