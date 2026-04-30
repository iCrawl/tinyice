# Plan: AutoDJ Ogg Metadata Propagation To MP3 Transcodes

> Source PRD: [2026-04-09-autodj-ogg-metadata-propagation-to-mp3-transcodes-design.md](../prds/2026-04-09-autodj-ogg-metadata-propagation-to-mp3-transcodes-design.md)

## Architectural decisions

Durable decisions that apply across all phases:

- **Routes**: Listener mounts remain the existing `/{mount}` HTTP endpoints. Browser-facing pages continue to use `/player/{mount}`, `/embed/{mount}`, and `/events` for UI metadata. No new public listener URLs are introduced in this feature.
- **Schema**: No database schema changes. Runtime metadata remains in relay stream state and relay metadata subscriptions.
- **Key models**: The source of truth for current-song state is the source mount's stream metadata. Transcoder output mounts are separate stream identities that inherit current-song state but keep their own output format and bitrate characteristics.
- **Metadata contract**: MP3 mounts continue to emit ICY metadata only when the listener requests it. Ogg listener streams remain pass-through and are not rewritten inline.
- **Scope boundary**: Phase 1 targets AutoDJ-produced Ogg source mounts built from MP3 files on disk. Generic external live Ogg ingest is deferred.
- **Testing stance**: Tests should assert observable mount state and listener-visible behavior, not internal goroutine or subscription implementation details.

---

## Phase 1: Source Mount Metadata Baseline

**User stories**: 1, 8, 10, 15, 21, 22

### What to build

Establish and verify the baseline source-mount behavior for the target path: an AutoDJ-produced Ogg source mount must expose stable current-song state that reflects the active file metadata. This slice is complete when the source mount itself can be trusted as the metadata source of truth for later phases.

### Acceptance criteria

- [ ] An AutoDJ-produced Ogg source mount exposes the expected current-song state after track activation.
- [ ] Missing or partial file metadata falls back gracefully without interrupting playback.
- [ ] The source mount's current-song state is visible through existing runtime/admin/API surfaces used for stream inspection.
- [ ] Test coverage proves source-mount metadata is published only after the real track becomes active.

---

## Phase 2: Transcoder Startup Backfill

**User stories**: 2, 7, 9, 16, 20, 24

### What to build

Add a metadata propagation path from source mount to transcoder output mount for the startup case. When an MP3 transcoder starts while the AutoDJ Ogg input mount is already mid-song, the output mount should immediately inherit the current-song state instead of waiting for a later change.

### Acceptance criteria

- [ ] Starting an MP3 transcoder from an AutoDJ Ogg input mount immediately seeds the output mount with the input mount's current song.
- [ ] The output mount keeps its own output identity while inheriting only current-song state.
- [ ] API/admin/runtime views of the output mount show the seeded current song after startup.
- [ ] Test coverage proves startup backfill works without relying on a new track boundary.

---

## Phase 3: Live Metadata Mirroring During Transcode

**User stories**: 2, 6, 9, 15, 17, 18, 21, 24

### What to build

Extend the startup backfill into continuous metadata mirroring for a running transcoder. When the AutoDJ Ogg source mount changes songs, the MP3 transcoder output mount should follow that change for the lifetime of the transcoder run, including clean behavior across stop, restart, and retry cycles.

### Acceptance criteria

- [ ] A metadata change on the input mount updates the transcoder output mount to the same current song.
- [ ] Restarting or retrying the transcoder does not duplicate metadata propagation or leave stale subscriptions behind.
- [ ] The output mount remains aligned with the input mount across multiple track changes in one run.
- [ ] Test coverage proves both live mirroring and clean shutdown/restart behavior.

---

## Phase 4: Listener-Facing ICY Validation On Transcoded MP3 Mounts

**User stories**: 3, 4, 11, 12, 13, 14, 19

### What to build

Validate the full listener-facing path now that transcoder output mounts have correct current-song state. An ICY-capable listener on the transcoded MP3 mount should receive the correct `StreamTitle`, while plain listeners and browser playback keep the existing safe behavior.

### Acceptance criteria

- [ ] An MP3 transcoder output emits correct ICY metadata when the listener negotiates ICY.
- [ ] The same mount does not emit ICY metadata for plain listeners that do not request it.
- [ ] Browser-safe playback behavior on the primary listener path remains unchanged.
- [ ] Test coverage proves the end-to-end path from AutoDJ source metadata to transcoded MP3 ICY output.

---

## Phase 5: Operational Hardening And Documentation

**User stories**: 5, 15, 21, 23, 24

### What to build

Harden the completed feature for operators and future work. Document the supported path clearly, confirm the first-phase scope boundary around AutoDJ-produced Ogg mounts, and leave the codebase with a reusable metadata propagation model for later derived outputs or external ingest extensions.

### Acceptance criteria

- [ ] Operator-facing docs describe the supported path: AutoDJ Ogg source mount to MP3 transcoder to ICY-capable listener.
- [ ] The implementation clearly preserves the scope boundary that external live Ogg ingest is not part of this rollout.
- [ ] The metadata propagation responsibility is isolated enough to be reused by future derived-output work.
- [ ] Residual risks and deferred follow-up work are documented for later phases.
