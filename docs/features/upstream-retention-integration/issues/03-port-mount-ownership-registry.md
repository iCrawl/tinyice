# Port Mount Ownership Registry

Status: needs-triage

Implementation: complete. Awaiting maintainer review. See [verification](../verification.md).

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Port RuntimeRegistry as the Mount Ownership Registry on top of upstream's lifecycle code. The registry should record which source owns a mount, which tenant owns it, and which runtime outputs are attached, without replacing upstream source-claim locking.

The completed slice should make source ownership visible to APIs and keep tenant live stream counts correct for Icecast, AutoDJ, relay pull, WebRTC, RTMP, SRT, and HLS output attachment.

## Acceptance criteria

- [x] The Mount Ownership Registry records source kind, tenant ownership, attached runtime outputs, and lifecycle timestamps.
- [x] Icecast, AutoDJ, relay pull, WebRTC, RTMP, SRT, and HLS output lifecycle paths attach and detach ownership metadata correctly.
- [x] Tenant live stream counts update when ownership moves between tenants or is removed.
- [x] Stream APIs can report source kind and human-readable source labels, including runtime-owned AutoDJ mounts without a remote source IP.
- [x] Registry reads return snapshots or otherwise avoid exposing mutable runtime state as the public read contract.
- [x] Upstream source claim and release behavior remains the authority for source-connection locking.

## Blocked by

- `docs/features/upstream-retention-integration/issues/01-upstream-first-integration-baseline.md`

