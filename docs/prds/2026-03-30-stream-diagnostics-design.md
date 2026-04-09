# Stream Diagnostics And Failure Visibility Design

## Goal

Make stream failures and stop conditions easy to understand without requiring log scraping.

The system should answer two operational questions directly in the admin and API:

1. Why is this mount dead, degraded, stopped, or recovering right now?
2. What recently happened to this mount that led to the current state?

This design covers both durable logging and admin-visible per-mount diagnostics.

## Problem Summary

Today the runtime emits useful logs in some places, but diagnostics are fragmented:

- health state is reduced to `healthy`, `degraded`, or `dead`
- many failures are only visible as transient logs
- the admin Streams page mostly infers status from `source_ip`
- there is no structured `last_error`, `reason`, or transition history per mount
- operators cannot easily distinguish a manual stop from an automatic stop
- AutoDJ command-driven mounts do not clearly explain whether the issue is playlist exhaustion, `song_command` failure, or recovery in progress

Logging already supports file output when configured, but the product does not maintain mount-scoped failure state that can be queried later.

## Goals

- Add a mount-scoped diagnostic model that tracks the latest operational state and reason.
- Keep a small recent history of transitions per mount.
- Expose diagnostics in `/api/streams` and `/api/autodj`.
- Show the latest reason directly in the admin UI.
- Continue writing structured logs so there is a durable record outside process memory.
- Distinguish manual operator stops from automatic runtime stops.
- Distinguish command-driven AutoDJ failures from playlist-based exhaustion.

## Non-Goals

- No persistent database-backed incident history in the first pass.
- No full-text log viewer in the admin.
- No generic alerting or notification system.
- No attempt to reconstruct historical diagnostics across process restarts beyond what log files already retain.

## Chosen Approach

Introduce a small in-memory diagnostic store keyed by mount, plus structured log emission at the same decision points.

Each mount gets:

- one `current` diagnostic snapshot
- one bounded `history` ring buffer, default size 10

The runtime updates diagnostics at the source of truth for each transition instead of trying to infer failures later from health metrics. The admin UI and APIs read from this shared diagnostic state.

This keeps the design narrow:

- the latest diagnostic is easy to render
- a short history explains how the mount got there
- file logs remain the durable forensic trail

## Diagnostic Model

### Current Snapshot

Each mount should expose a latest diagnostic record with fields along these lines:

- `mount`
- `status`
- `class`
- `reason`
- `error`
- `actor`
- `updated_at`
- `last_recovery_at`
- `last_recovery_result`

### Status Values

The status should be operational and admin-friendly rather than transport-specific only:

- `running`
- `degraded`
- `dead`
- `recovering`
- `stopped`
- `error`

### Classification Values

The class should explain why the status exists. Initial examples:

- `manual_stop`
- `automatic_stop`
- `source_disconnect`
- `health_degraded`
- `health_dead`
- `song_command_failure`
- `song_command_empty_output`
- `song_command_invalid_file`
- `playlist_exhausted`
- `startup_failure`
- `recovery_started`
- `recovery_succeeded`
- `recovery_failed`

### Actor Values

The actor identifies who or what set the state:

- `admin`
- `health_monitor`
- `autodj`
- `relay`
- `icecast_source`
- `webrtc`
- `rtmp`
- `srt`
- `system`

### History Entries

Each history entry should capture:

- `timestamp`
- `status`
- `class`
- `reason`
- `error`
- `actor`
- optional `details`

History should be bounded in memory using a simple ring buffer per mount. The first pass should default to 10 entries.

## Failure Classification Rules

### Manual Versus Automatic Stop

The runtime must preserve the distinction between:

- manual stop initiated by an operator
- internal stop caused by exhaustion, disconnect, startup failure, or dead-health handling

Manual stop is terminal and should suppress automatic restart. Automatic stop should remain eligible for recovery logic where appropriate.

### AutoDJ Playlist Versus `song_command`

If `song_command` is configured, empty playlist state should not be treated as the primary failure reason.

Instead, diagnostics should prefer command-path reasons such as:

- `song_command failed`
- `song_command returned empty output`
- `song_command returned invalid file`
- `recovering after dead health event`

`playlist_exhausted` should only be used when there is no usable `song_command` path for that mount.

## Update Sources

Diagnostics should be written at the actual transition points.

### Health Monitor

When health changes:

- set `degraded` with reason like `no data for 11s`
- set `dead` with reason like `no data for 34s`

This should not only log the state change; it should update the mount’s current diagnostic and append a history entry.

### AutoDJ

Update diagnostics when:

- streamer is manually stopped
- streamer stops automatically because it has no playable source
- `song_command` fails
- `song_command` returns empty output
- `song_command` returns an invalid path or invalid file
- recovery starts
- recovery succeeds
- recovery fails
- startup/output session creation fails

### Ingest And Source Paths

Update diagnostics when:

- a source connects
- a source disconnects
- a relay pull drops
- RTMP, WebRTC, or SRT ingest ends unexpectedly
- a mount is removed

This ensures live mounts and AutoDJ mounts share a common diagnostic surface.

## API Changes

### `/api/streams`

Extend the existing stream payload to include the latest diagnostic and recent history.

Recommended additions:

- `status`
- `status_class`
- `status_reason`
- `last_error`
- `status_updated_at`
- `history`

For inactive configured mounts that do not currently have a live relay stream, diagnostics should still be returned when available.

### `/api/autodj`

Extend the AutoDJ payload similarly so studio/admin screens can explain why an instance is stopped or recovering.

Recommended additions:

- `status`
- `status_class`
- `status_reason`
- `last_error`
- `status_updated_at`
- `history`

## Admin UI Changes

### Streams Page

Replace the current implicit status dot with explicit operational information:

- status badge: `Running`, `Degraded`, `Dead`, `Recovering`, `Stopped`, `Error`
- one-line reason under or beside the mount
- optional expandable recent history list per row

Examples:

- `Dead` — `No data for 63s`
- `Stopped` — `Manual stop by admin`
- `Recovering` — `Retrying song_command after dead health event`
- `Error` — `song_command returned invalid file`

### AutoDJ Views

AutoDJ screens should reuse the same diagnostic language so a command-driven mount can clearly show whether it is:

- stopped manually
- out of playable sources
- failing on `song_command`
- currently recovering

## Logging

File logging remains important even after UI/API diagnostics are added.

### Logging Behavior

- Keep using structured logs through the existing logger.
- Ensure all major state transitions also log mount, actor, status, class, and reason.
- Reuse the configured log file when provided; do not invent a separate diagnostics file format in the first pass.

### Relationship To In-Memory Diagnostics

The log file is the durable trail.
The in-memory diagnostic store is the operator-facing current view plus short recent history.

If the process restarts:

- the log file still has the full sequence
- in-memory diagnostic history resets

That trade-off is acceptable for the first pass.

## Data Ownership

The diagnostic state should live in a shared runtime component accessible from both relay/server code paths. It should not be embedded only in `StreamStats`, because many diagnostics apply even when no active relay stream object exists.

A dedicated manager owned by the server or relay layer is preferable to overloading raw stream structs with UI-facing incident history.

## Testing

Add focused coverage for:

- health monitor writes degraded and dead reasons
- manual stop records `manual_stop`
- automatic stop records non-manual stop state
- `song_command` failures record the correct class and reason
- `playlist_exhausted` is only used when no `song_command` is configured
- successful recovery transitions from `dead` or `recovering` to `running`
- `/api/streams` includes current diagnostic plus history
- `/api/autodj` includes current diagnostic plus history

## Risks

### Overwriting Useful Context

If every minor event rewrites the current status, the latest snapshot may become noisy. The implementation should update the current record only for meaningful transitions and append all meaningful transitions to history.

### Confusing Status Vocabulary

If health state and lifecycle state are mixed carelessly, the UI can become ambiguous. The implementation should keep `status` human-readable and `class` specific enough to explain why.

### Too Much UI Density

The Streams table should show the latest reason clearly without turning into a log viewer. Recent history should be collapsible.

## Implementation Notes

Likely touch points:

- `relay/health.go`
- `relay/streamer.go`
- live ingest and disconnect paths in `server` and `relay`
- `/api/streams` in `server/handlers_api_v2.go`
- `/api/autodj` in `server/handlers_api_v2.go`
- admin streams UI in `server/frontend/src/pages/admin/Streams.tsx`
- shared frontend types in `server/frontend/src/types.ts`
- logger call sites for structured transition logging

## Summary

The first pass should deliver:

- structured file logs for stream lifecycle and failure transitions
- per-mount current diagnostic state
- per-mount recent history ring buffer
- admin/API visibility into why a mount is dead, stopped, degraded, or recovering
- correct differentiation between manual stop, automatic stop, playlist exhaustion, and `song_command` failure
