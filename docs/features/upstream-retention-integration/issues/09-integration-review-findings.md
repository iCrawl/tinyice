# Integration Review Findings

Status: needs-triage

Implementation: complete. Awaiting maintainer review. See [verification](../verification.md).

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to repair

Independent review and actual operator journeys found defects in the first combined port. Repair the ownership and public-contract causes before completing integration.

## Acceptance criteria

- [x] Removed AutoDJ sessions cannot publish into a replacement source. Removal cancels the old producer and publication checks its exact claim.
- [x] Connected HTTP listeners wait when primary and fallback are both unavailable. Outage limits and disconnect cancellation remain effective.
- [x] Turning metadata injection off preserves internal real-track activation time and identity.
- [x] Combined stream snapshots reconcile authoritative playback revisions, including unlisted tracks and terminal clears. Metadata-only visibility remains enforced.
- [x] New-mount Enabled and Visible controls persist through the real create handler.
- [x] Configured AutoDJ-only mounts accept authorized cap, burst, visibility, and enabled edits. Disabled mounts remain listed and cannot resume through Studio or MPD until re-enabled.
- [x] Delayed history success, failure, and loading updates cannot affect a newer or closed dialog, including reopening the same mount.
- [x] Explicit source disconnect and intentional pause/stop remain distinguishable from producer health failures.
- [x] AutoDJ diagnostics and recovery state are available through both stream and AutoDJ APIs.
- [x] Studio playlist save/load and metadata actions return usable successful API responses.
- [x] Focused event and listener files share one metadata event serialization policy.

## Evidence

The October verification record and committed decision trail record the repaired revisions, independent reviews, and runtime checks. Regressions must call the production encoder, handlers, or frontend callbacks.
