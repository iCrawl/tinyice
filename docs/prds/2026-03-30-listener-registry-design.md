# Listener Registry And Playback Client Control Design

## Goal

Add a first-class runtime listener model so TinyIce can:

1. list active playback clients accurately
2. enforce per-mount listener caps
3. disconnect individual playback clients
4. support controlled listener moves as a follow-up on top of the same model

This design is intentionally narrower than full Icecast admin parity. It targets playback listeners only because that is the smallest change that unlocks the features we actually want.

## Problem Summary

Today TinyIce tracks listeners as opaque signal channels attached to a stream:

- the runtime can count listeners
- the runtime can disconnect all listeners on a stream
- the runtime cannot enumerate rich per-listener state
- the runtime cannot target a single listener for disconnection
- the runtime cannot move a listener from one mount to another through an explicit admin action

The current model is enough for streaming, but not enough for admin-level client management.

## Goals

- Introduce a first-class playback listener registry.
- Preserve current HTTP and WebRTC playback behavior.
- Add per-mount listener caps as the first mount-policy parity improvement.
- Expose active listener snapshots through TinyIce API and admin UI.
- Support individual listener disconnect.
- Prepare the runtime for listener move support without requiring a second redesign later.

## Non-Goals

- No full connection registry for source clients, relays, RTMP, SRT, or WebRTC ingest in the first pass.
- No attempt to fully emulate Icecast's legacy admin XML surface in the first pass.
- No billing, rate-limit, or authentication policy changes for listeners in this work.
- No persistence of listener sessions across process restarts.

## Chosen Scope

The registry should cover playback listeners only:

- progressive HTTP listeners served by `handleListener`
- WebRTC playback listeners served by the WebRTC playback path

This is the right first boundary because:

- listing and moving listeners are playback concerns
- per-mount listener caps apply to playback clients
- source and ingest protocols have different semantics and would expand scope sharply

Sources, relays, RTMP, SRT, and WebRTC ingest can be added later if a broader "connection registry" becomes necessary.

## Chosen Approach

Introduce a runtime `Listener` object and store active listeners in a registry instead of only keeping bare signal channels in `Stream.listeners`.

Each listener object should own:

- identity and snapshot metadata
- current mount and protocol
- connection timing data
- transport-specific control hooks for disconnect or move
- subscription state required by the streaming loop

Streams should continue to own their local listener membership, but they should store listener objects or listener references rather than anonymous channels.

The server should gain one higher-level registry view so admin and API code can ask:

- which listeners are active right now
- which mount each listener belongs to
- how many listeners are attached to a mount
- how to disconnect or move one listener

## Listener Model

### Shared Listener Fields

Each listener should expose at least:

- `id`
- `protocol` with values like `http` or `webrtc`
- `requested_mount`
- `current_mount`
- `remote_addr`
- `user_agent`
- `connected_at`
- `last_stream_switch_at`

Recommended internal fields:

- `stream`
- `offset`
- `signal`
- `disconnect_ch`
- `move_ch`

The runtime fields do not all need to be exposed externally, but the model should be rich enough that admin operations do not depend on parsing synthetic IDs.

### Protocol-Specific Extensions

HTTP listeners need:

- current stream subscription
- current ICY metadata emission state required by the existing loop
- a control path that can tell the serve loop to switch mounts or disconnect

WebRTC listeners need:

- the current playback mount
- a disconnect hook
- a protocol-specific move strategy

The first pass should use one shared listener snapshot shape with optional protocol-specific internal fields rather than separate public APIs for each protocol.

## Registry Structure

Two valid implementations fit the design:

1. `Stream.listeners` becomes `map[string]*Listener`
2. `Stream.listeners` stores listener IDs while a relay- or server-level registry stores the full objects

The design should prefer the option that minimizes lock inversion and keeps stream broadcast paths simple.

Recommended direction:

- keep mount-local listener membership on the stream
- add a server- or relay-level registry for global lookup by listener ID

This gives efficient mount counting and also makes `/api/listeners` style endpoints straightforward.

## Mount-Level Listener Caps

Add `MaxListeners` to mount settings and enforce it before a playback listener is admitted.

Rules:

- if a mount-specific limit is set, it takes precedence
- otherwise fall back to the current global `Config.MaxListeners`
- a zero mount-specific value means "use global behavior"
- if the effective limit is exceeded, reject the new listener with `503`

The first pass should apply this to playback listeners only.

## Listing Listeners

The admin/API list should be a point-in-time snapshot generated from the registry.

Recommended response fields:

- `id`
- `protocol`
- `requested_mount`
- `current_mount`
- `remote_addr`
- `user_agent`
- `connected_at`
- `duration_seconds`

The list should be sorted by `connected_at`, newest first.

The admin UI can start with a read-only list plus disconnect controls.

## Disconnecting One Listener

Individual disconnect should be supported as an explicit registry action.

Behavior:

- find listener by ID
- close only that listener
- remove it from the registry and its stream membership
- allow the serve loop or protocol handler to exit cleanly

This should not depend on removing the whole stream or dropping every listener on the mount.

## Moving One Listener

### Definition Of "Move Properly"

A proper move means an admin action targets one active playback listener and tells it to continue playback from a different mount without treating the source mount as failed for everyone else.

### HTTP Listener Strategy

HTTP listeners can support real moves inside the existing connection if the serve loop gains a control path.

Recommended behavior:

- admin issues move request with `listener_id` and `target_mount`
- listener control channel notifies the active loop
- current stream subscription is released
- new target stream is looked up and subscribed
- playback resumes from the target stream using the normal subscription logic

This is more correct than abusing fallback mounts because it is:

- listener-specific
- explicit
- independent of source failure

### WebRTC Listener Strategy

WebRTC playback moves are riskier because track and peer connection behavior differs from the HTTP stream loop.

The first pass should allow one of two outcomes:

- if track rebinding is already clean and low risk, support seamless move
- otherwise implement `disconnect_required` for WebRTC move attempts and keep the API honest

The design should not block the registry on having seamless WebRTC moves on day one.

## API Surface

Recommended TinyIce-native endpoints:

- `GET /api/listeners`
- `POST /api/listeners/disconnect`
- `POST /api/listeners/move`

Suggested request and response shapes:

### `GET /api/listeners`

Returns an array of listener snapshots.

### `POST /api/listeners/disconnect`

Request:

- `id`

Response:

- `status`

### `POST /api/listeners/move`

Request:

- `id`
- `target_mount`

Response:

- `status`
- `current_mount`
- optional `warning` if the protocol cannot move seamlessly

## Admin UI

The first UI should stay small:

- new listeners table in admin
- filter by mount
- show protocol, IP, age, user-agent
- disconnect action
- move action

Move can ship after the list and disconnect views if sequencing needs to stay conservative.

## Runtime And Concurrency Notes

The critical constraint is that streaming remains safe under concurrent admin actions.

The implementation should ensure:

- broadcast paths do not iterate over mutable listener state in unsafe ways
- registry removal is idempotent
- move does not leak old subscriptions
- disconnect and move cannot race into double-close panics
- Ogg/Opus listener setup still emits headers correctly after a move or resubscribe

The serve loop should own the transition between streams. Admin code should request actions through control channels rather than mutating a listener's stream from the outside.

## Failure Semantics

Disconnect failures:

- if listener no longer exists, return `404`
- if listener is already closing, return success or a stable no-op status

Move failures:

- if target mount does not exist, return `404`
- if target mount is full, return `409` or `503`
- if protocol cannot move seamlessly in this phase, return a structured warning or error instead of pretending success

## Observability

Add structured logs for:

- listener connected
- listener disconnected
- listener moved
- listener move failed
- listener rejected due to mount cap

This keeps client-control actions visible in logs alongside the existing stream logs.

## Phase Plan

### Phase 1

- introduce listener object and registry
- migrate HTTP playback listeners onto it
- add mount-level listener caps
- keep existing list/count behavior working

### Phase 2

- expose listener list and per-listener disconnect in API
- add admin UI for active listeners

### Phase 3

- implement HTTP per-listener move
- decide whether WebRTC move is seamless or reported as reconnect-required

## Risks

- touching listener lifecycle code can cause leaks or double-close bugs
- move support can mishandle Ogg header replay if resubscription is not carefully scoped
- WebRTC playback may need a narrower initial move story than HTTP playback

The phased approach reduces this risk by making list/disconnect land before move.

## Success Criteria

This work is successful when:

- TinyIce can list active playback listeners with stable IDs and metadata
- operators can disconnect one listener without dropping the whole mount
- mount-specific listener caps can be configured and enforced
- HTTP playback listeners can be moved between mounts through an explicit control path, or the implementation returns an honest protocol limitation
