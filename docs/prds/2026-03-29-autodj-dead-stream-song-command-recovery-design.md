# AutoDJ Dead Stream Song Command Recovery — Design Spec

## Goal

When an AutoDJ-managed mount with a configured `song_command` is marked `dead` by the health monitor, attempt recovery by rerunning `song_command` with exponential backoff instead of waiting indefinitely for manual intervention.

This design is intentionally narrow:

- only AutoDJ mounts are in scope
- only mounts with a non-empty `song_command` are in scope
- the trigger is the existing health monitor's `dead` event

## Problem Summary

Today `song_command` is only consulted during normal AutoDJ track selection. If it fails, the streamer logs the error and falls back to playlist selection for that iteration.

Separately, the health monitor can classify a mount as `dead`, but that state change is currently informational only. There is no AutoDJ-specific recovery path tied to the `dead` event.

That leaves a gap for command-driven AutoDJ mounts:

1. the mount stops producing data
2. the health monitor marks it `dead`
3. nothing retries the command because normal track selection is no longer enough to bring the mount back

## Non-Goals

- No generic "dead stream restart" framework for all source types.
- No change to manual source ingest, relays, transcoders, WebRTC, or SRT recovery behavior.
- No change to the meaning of health states.
- No new user-facing configuration for retry policy in this iteration.
- No automatic rerun for AutoDJ mounts that do not use `song_command`.

## Chosen Approach

Use the existing health monitor event stream as the trigger and add a guarded AutoDJ recovery path that reuses the existing relay `backoff` helper.

When the server receives a `dead` health event:

1. determine whether the mount belongs to an AutoDJ streamer
2. ignore the event unless that streamer has a non-empty `song_command`
3. start a single recovery worker for that mount if one is not already running
4. have that worker retry `song_command` with the shared relay backoff policy
5. stop retrying as soon as the mount becomes healthy again, the streamer stops, or recovery succeeds and normal playback resumes

This keeps the design aligned with existing supervision patterns in the codebase instead of inventing a second recovery model.

## Rejected Alternatives

### 1. Retry immediately on every `song_command` failure

This is too eager. The request here is specifically to avoid weird behavior and only intervene once the mount is actually considered `dead`.

### 2. Copy the transcoder retry loop

That would work, but it duplicates logic and loses the jittered helper that already exists for relay reconnects.

### 3. Add configurable retry knobs now

Possible later, but premature for the current problem. The codebase already has a reusable backoff policy that is sufficient for a first pass.

## Trigger Model

The health monitor remains the sole trigger source.

The server already subscribes to health monitor events. Extend that callback so that when `NewStatus == StatusDead`, the server asks the `StreamerManager` to attempt AutoDJ dead-stream recovery for that mount.

Behavioral rules:

- `healthy -> degraded`: do nothing
- `degraded -> dead`: eligible to trigger recovery
- repeated `dead` observations for the same mount: do not launch duplicate recovery workers
- non-AutoDJ mounts: do nothing
- AutoDJ mounts without `song_command`: do nothing

## Recovery Ownership

Recovery should be owned by `StreamerManager` and keyed by mount.

Why:

- the server has the health event but not the detailed AutoDJ lifecycle state
- the manager already owns AutoDJ instances by mount
- duplicate suppression is naturally manager-scoped

Add manager-owned recovery bookkeeping so only one dead-stream recovery worker can run per mount at a time.

The worker should be cancellable when:

- the streamer is stopped
- the streamer is removed
- the process shuts down
- the mount has already recovered through normal playback

## Recovery Worker Flow

The recovery worker should follow this loop:

1. Confirm the streamer still exists, is AutoDJ-managed, and still has `song_command`.
2. Check whether the mount is already producing fresh audio again. If yes, exit immediately.
3. Run `execSongCommand()`.
4. If the command fails, wait for `backoff.next()` and retry.
5. If the command returns a valid file, attempt to activate it through the existing AutoDJ playback path rather than inventing a parallel direct-to-stream write path.
6. If activation succeeds and the mount resumes output, clear recovery state and exit.
7. If activation fails, apply backoff and retry until cancellation or recovery.

The worker is not a new playback engine. It is only a bridge that nudges the existing AutoDJ machinery back into a working track source.

## Interaction With The Existing AutoDJ Loop

The existing AutoDJ loop remains the source of truth for:

- whether the streamer is playing
- output session ownership
- metadata updates
- track activation mechanics
- silence fallback semantics

The recovery path should reuse those mechanisms instead of bypassing them.

That means the recovery worker should:

- avoid creating a second output session
- avoid writing directly to the relay stream
- avoid duplicating queue and playlist sequencing logic
- only coordinate enough state to let the current output session receive a fresh valid track source again

If the output session is already alive but silent, recovery should swap in a real track source through the same path normal playback uses.

## Backoff Policy

Reuse the relay `backoff` helper exactly as the first implementation.

Current helper behavior:

- exponential growth
- capped maximum
- jitter
- reset support

This is a good fit because dead AutoDJ mounts can otherwise synchronize retries if several command-driven mounts fail at once.

No new config is introduced in this pass. If the default relay policy turns out to be too aggressive or too slow for AutoDJ, that can be revisited later with real operational feedback.

## Recovery Success Criteria

Recovery is considered successful when all of the following are true:

- a valid file has been obtained from `song_command`
- the file has been activated through the existing AutoDJ path
- the mount resumes producing fresh data

Success should stop the worker and clear its in-flight marker so a future dead event can trigger a new recovery cycle if needed.

## Failure Handling

### Command failure

If `execSongCommand()` fails, log the failure with the mount and continue retrying with backoff.

### Activation failure

If a valid command result is returned but the file cannot be activated, log the failure and continue retrying with backoff.

### Streamer no longer playable

If the streamer is stopped or removed while recovery is running, cancel recovery and clear its bookkeeping.

### Natural recovery

If normal AutoDJ playback resumes before the worker succeeds, the worker should detect that the mount is healthy again and exit quietly without forcing another activation.

## Observability

Add focused logs for:

- dead-event-triggered recovery start
- skipped recovery because mount is not AutoDJ or has no `song_command`
- duplicate dead event ignored because recovery is already running
- command retry with backoff duration
- successful recovery
- recovery canceled because streamer stopped or mount recovered naturally

These logs should make it obvious whether the system is attempting recovery and why it stopped.

## Testing

### Unit tests

Add tests for:

- `dead` event on a `song_command` AutoDJ mount starts recovery
- repeated `dead` events for the same mount do not create multiple workers
- AutoDJ mounts without `song_command` do not start recovery
- non-AutoDJ mounts do not start recovery
- successful recovery clears the in-flight recovery marker
- recovery exits when the mount becomes healthy through normal playback before the worker succeeds

### Integration-oriented tests

Add coverage for:

- a command-driven AutoDJ mount that goes silent long enough to be marked `dead`, then resumes after a retried `song_command`
- no duplicate activation or overlapping recovery loops during repeated dead checks

## Risks

### Parallel activation races

If the recovery worker and the normal AutoDJ loop both try to activate a track at the same time, behavior can become nondeterministic. The implementation should make one path authoritative for actual source activation and use recovery only to feed that path.

### Over-retrying a permanently broken command

Backoff reduces pressure, but a permanently broken `song_command` will still keep retrying until the streamer stops or the mount recovers. This is acceptable for the first pass because it is scoped to explicitly configured command-driven AutoDJ mounts and is visible in logs.

### Misidentifying recovery

The worker must use a reliable "fresh data is flowing again" check rather than assuming a successful command invocation means the mount is healthy.

## Implementation Notes

Likely touch points:

- `relay/health.go` for event semantics reference only, not for behavior changes
- `server/server.go` to connect dead health events to AutoDJ recovery
- `relay/streamer.go` to add manager-level recovery ownership and worker logic
- existing AutoDJ tests plus new recovery-specific tests

The health monitor remains passive except for invoking the AutoDJ-specific recovery hook through the server callback.
