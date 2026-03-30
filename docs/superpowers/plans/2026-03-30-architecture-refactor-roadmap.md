# TinyIce Architecture Refactor Roadmap

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this roadmap task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce architectural drift, eliminate known shared-state and contract issues, and leave TinyIce with clearer subsystem boundaries that are safer to extend.

**Architecture:** This is an umbrella refactor roadmap, not a single atomic implementation plan. The work is intentionally split into sequenced tracks so that correctness and contract fixes land before structural cleanup. Each track should produce a working system and preserve the current external feature set.

**Tech Stack:** Go 1.25, net/http, Preact, TypeScript, Vite, SQLite/GORM, SSE, embedded frontend assets

---

## Why This Is Split

This refactor spans multiple semi-independent subsystems:

1. Streaming and AutoDJ concurrency correctness
2. Server-side event and API contract cleanup
3. Config/state ownership and mutation boundaries
4. Server package decomposition
5. Frontend state and data contract cleanup
6. Documentation and dead-path cleanup

Trying to execute all of that as one branch would create unnecessary merge risk and make regressions hard to localize. The recommended approach is:

- Phase 1 first, alone
- Phase 2 second, alone
- Phase 3 third, alone
- Phases 4-6 only after the first three are green

---

## Constraints

- Do not change external user-facing behavior unless the roadmap explicitly calls for a contract correction.
- Keep `go test ./...` green after every phase.
- Add race-detector verification to the critical Go packages before claiming the refactor is safe.
- Preserve current REST paths unless explicitly replacing duplicates and providing migration coverage.
- Avoid broad rewrites in the frontend; fix contracts and state ownership first.

---

## Current Problem Summary

### 1. Concurrency debt

- `go test -race ./server ./relay` currently fails in AutoDJ/streaming paths.
- Shared mutable state is read and written from goroutines with inconsistent locking.
- Config persistence is also mutated from multiple paths with partial locking.

### 2. Contract drift

- The admin frontend consumes SSE events with shapes and endpoints that do not cleanly match the server implementation.
- Public and admin event streams are conceptually distinct but not consistently used as such.

### 3. Architectural drift

- Runtime serves the new embedded Preact shell, but the docs and parts of the server still describe or initialize a legacy template stack.
- `server` has become the coordinator for too many responsibilities.
- `relay` now contains an unfinished pipeline abstraction alongside the older relay model.

### 4. Frontend state drift

- Several pages use module-level singleton signals for page state.
- That is convenient for the current app shape, but brittle for route transitions, tests, and future extraction.

---

## Phase Order

### Phase 1: Concurrency and Correctness Baseline

**Outcome:** The known shared-state hazards are removed or isolated enough that `go test -race ./server ./relay` becomes meaningful and stable.

**Files to inspect and likely modify:**

- `relay/stream.go`
- `relay/buffer.go`
- `relay/streamer.go`
- `relay/autodj_output_session.go`
- `relay/transcode.go`
- `server/auth.go`
- `server/review_regressions_test.go`
- `relay/*_test.go`

**Primary issues to address:**

- Normalize access to `Stream.LastDataReceived` and related stream fields behind lock-safe methods.
- Eliminate any direct reads of mutable stream internals from background recovery loops.
- Review `CircularBuffer` interaction patterns used by output sessions and tests to ensure readers are not inspecting internals without synchronization.
- Audit AutoDJ output session lifecycle to ensure source replacement, cancellation, and encoder teardown are serialized.
- Ensure debounced token persistence in `server/auth.go` cannot race with other config writes.

**Implementation tasks:**

- [ ] Introduce lock-safe accessor methods for mutable `Stream` fields that are currently observed from goroutines.
- [ ] Replace raw field reads in dead-recovery and AutoDJ loops with those accessors.
- [ ] Review any tests that currently touch mutable internal buffer or stream fields directly and convert them to safe probes.
- [ ] Add focused regression tests for:
  - dead-mount recovery exiting when new stream data appears
  - AutoDJ output session source switching
  - token usage persistence coexisting with config mutation
- [ ] Run `go test ./relay ./server`
- [ ] Run `go test -race ./relay ./server`

**Exit criteria:**

- `go test ./relay ./server` passes
- `go test -race ./relay ./server` passes or only fails in a documented third-party encoder path that is isolated from TinyIce-owned state
- No TinyIce-owned data race remains in the report

**Commit boundary:**

- `refactor: make relay and autodj state access race-safe`

---

### Phase 2: SSE and API Contract Normalization

**Outcome:** The frontend and backend share explicit, stable event shapes and the admin app uses the admin event stream consistently.

**Files to inspect and likely modify:**

- `server/handlers_api.go`
- `server/handlers_api_v2.go`
- `server/server.go`
- `server/frontend/src/types.ts`
- `server/frontend/src/lib/sse.ts`
- `server/frontend/src/pages/admin/Dashboard.tsx`
- `server/frontend/src/pages/admin/AutoDJ.tsx`
- `server/frontend/src/pages/admin/Studio.tsx`
- `server/frontend/src/pages/Player.tsx`
- `server/frontend/src/pages/Landing.tsx`
- `server/frontend/src/pages/Explore.tsx`
- `server/frontend/src/pages/Embed.tsx`

**Primary issues to address:**

- Admin pages currently subscribe to `/events` even though `/admin/events` is the admin stream.
- Event payload names differ across server and frontend types.
- The same conceptual stream state is emitted in different partial shapes.

**Contract decisions for this phase:**

- Keep two streams:
  - `/events` for public, no auth
  - `/admin/events` for authenticated admin data
- Define explicit event payload types:
  - `metadata`
  - `stream`
  - `stats`
  - `autodj`
  - `streams` only if public list pages truly need it
- Match wire field names exactly in Go and TypeScript. No local “best guess” remapping in page components.

**Implementation tasks:**

- [ ] Define canonical admin SSE payload structs in Go near the event handlers.
- [ ] Define canonical frontend TS interfaces that match those structs exactly.
- [ ] Update `/admin/events` to emit a single stable shape for `stream` and `autodj`.
- [ ] Update admin pages to subscribe to `/admin/events`.
- [ ] Update public pages to subscribe only to the fields emitted by `/events`.
- [ ] Simplify `createSSE` so reconnect logic and listener registration do not create duplicate event handlers.
- [ ] Add or expand tests around:
  - admin event stream payloads
  - public event stream payloads
  - frontend helpers that map traffic and stream events

**Verification:**

- `go test ./server`
- `node --test server/frontend/src/**/*.test.mjs`

**Commit boundary:**

- `refactor: normalize public and admin SSE contracts`

---

### Phase 3: Centralize Config Mutation and Persistence

**Outcome:** Config reads and writes have one ownership model instead of ad hoc mutation plus scattered `SaveConfig()` calls.

**Files to inspect and likely modify:**

- `config/config.go`
- `server/server.go`
- `server/auth.go`
- `server/auth_oidc.go`
- `server/auth_passkey.go`
- `server/handlers_admin.go`
- `server/handlers_api_v2.go`
- `server/handlers_player.go`
- `server/handlers_pending_users.go`
- `server/handlers_relay.go`
- `server/handlers_setup.go`

**Primary issues to address:**

- `Server.configMu` exists but is not the single guard for config writes.
- `Config.SaveConfig()` is called directly from many handlers.
- Token usage updates and other background writes can overlap with foreground admin writes.

**Target shape:**

- Introduce a small server-side config service/store with methods like:
  - `Read(func(*config.Config) error) error`
  - `Update(func(*config.Config) error) error`
  - `Persist() error` handled internally by `Update`
- All writes go through one API.
- Handlers stop mutating `s.Config` directly when persistence is involved.

**Implementation tasks:**

- [ ] Add a `ConfigStore` or `ConfigService` in `server/` with one lock governing mutation and persistence.
- [ ] Migrate the highest-risk write paths first:
  - auth token last-used writes
  - setup completion
  - pending user approval/denial
  - AutoDJ/relay/transcoder CRUD
- [ ] Migrate remaining handler write paths.
- [ ] Leave plain read-only access alone unless needed.
- [ ] Add tests covering concurrent updates to independent config sections.

**Verification:**

- `go test ./server`
- `go test -race ./server`

**Commit boundary:**

- `refactor: centralize config mutation and persistence`

---

### Phase 4: Decompose the Server Package by Domain

**Outcome:** `server` stops acting as one giant application file set and becomes easier to reason about per domain.

**Files to inspect and likely modify:**

- `server/server.go`
- `server/handlers_api_v2.go`
- `server/handlers_api.go`
- `server/handlers_admin.go`
- `server/handlers_player.go`
- `server/handlers_relay.go`
- `server/handlers_public.go`
- `server/middleware.go`

**Primary issues to address:**

- `NewServer()` wires almost every subsystem directly.
- Route registration is a large flat list.
- `handlers_api_v2.go` is too large and spans too many concerns.

**Recommended decomposition:**

- `server/app.go`
  - `Server` construction and shared dependencies
- `server/routes.go`
  - route registration only
- `server/api_streams.go`
- `server/api_autodj.go`
- `server/api_relays.go`
- `server/api_transcoders.go`
- `server/api_security.go`
- `server/api_settings.go`
- `server/api_auth.go`
- `server/events_admin.go`
- `server/events_public.go`

**Implementation tasks:**

- [ ] Move route wiring out of `server.go`.
- [ ] Split `handlers_api_v2.go` by domain without changing the route surface.
- [ ] Extract small helper services where duplication is obvious:
  - mount visibility and access enumeration
  - diagnostics lookup
  - audit logging
- [ ] Keep behavior identical while reducing file size and mixed responsibilities.

**Verification:**

- `go test ./server`

**Commit boundary:**

- `refactor: split server handlers by domain`

---

### Phase 5: Resolve Legacy UI Drift and Frontend State Ownership

**Outcome:** The repo has one clear frontend architecture and less brittle page state.

**Files to inspect and likely modify:**

- `server/server.go`
- `server/shell.go`
- `server/templates/*.html`
- `server/frontend/src/pages/**/*.tsx`
- `server/frontend/src/lib/*.ts`
- `ARCHITECTURE.md`
- `README.md`

**Primary issues to address:**

- The server still parses legacy templates even though the runtime pages use the embedded Preact shell.
- Docs still describe SSR templates plus vanilla JS as the web architecture.
- Several pages hold long-lived UI state in module-level signals.

**Recommended decisions:**

- Treat the Preact shell as the primary UI runtime.
- Keep legacy templates only if they still serve a real route that is not yet ported.
- Otherwise remove dead template initialization and document what remains.
- For the frontend, move page state into small local stores/factories where pages currently rely on module singletons.

**Implementation tasks:**

- [ ] Confirm whether any embedded Go templates are still rendered. Remove dead parse/init code if not.
- [ ] Delete or quarantine unused templates after verifying no route depends on them.
- [ ] For the largest stateful pages, introduce page-local store creators:
  - `Dashboard`
  - `Studio`
  - `AutoDJ`
  - `Player`
- [ ] Keep Preact Signals, but scope them to a page instance instead of module lifetime where practical.
- [ ] Update docs to describe the actual runtime architecture.

**Verification:**

- `go test ./server`
- `node --test server/frontend/src/**/*.test.mjs`
- `npm --prefix server/frontend run build`

**Commit boundary:**

- `refactor: align frontend runtime and page state ownership`

---

### Phase 6: Decide the Fate of the Pipeline Abstraction

**Outcome:** The codebase either commits to `Pipeline` as the future runtime abstraction or clearly parks it as an isolated experimental layer.

**Files to inspect and likely modify:**

- `relay/pipeline.go`
- `relay/pipeline_manager.go`
- `relay/pipeline_manager_test.go`
- `relay/interfaces.go`
- `relay/tenant.go`
- `ARCHITECTURE.md`

**Current issue:**

- `PipelineManager` exists as a wrapper around `Relay` for backward compatibility, but the runtime is still centered on `Relay`.
- That creates two overlapping mental models for contributors.

**Decision options:**

- Option A, recommended near-term: keep `Relay` as the primary runtime abstraction and label pipeline code as internal groundwork, not active architecture.
- Option B, later: promote pipeline as primary and migrate server/runtime consumers in a dedicated project.

**Implementation tasks for Option A:**

- [ ] Remove or downgrade interfaces/docs that imply `PipelineManager` is the main runtime path.
- [ ] Keep tenant-owned pipelines where they are genuinely used.
- [ ] Update architecture docs to state that `Relay` is the live runtime abstraction today.

**Implementation tasks for Option B:**

- [ ] This should be a separate design/spec/plan cycle after Phases 1-5.

**Verification:**

- `go test ./relay`

**Commit boundary:**

- `docs: clarify runtime relay versus pipeline abstractions`

---

## Recommended Execution Slices

### Slice A: Safety First

- Phase 1
- Phase 2
- Phase 3

This is the minimum worthwhile refactor set. It removes the largest correctness and maintainability risks without forcing large-scale file churn.

### Slice B: Structural Cleanup

- Phase 4
- Phase 5

Only start this after Slice A is green and stable.

### Slice C: Strategic Abstraction Decision

- Phase 6

Do this only when the team wants to either invest in pipelines or explicitly reduce abstraction noise.

---

## Verification Matrix

Run these after each phase as applicable:

```bash
go test ./...
go test -race ./server ./relay
node --test server/frontend/src/**/*.test.mjs
npm --prefix server/frontend run build
```

Additional manual checks:

- Admin dashboard loads and receives live updates
- AutoDJ page updates from SSE without reload
- Studio page shows the selected mount and reacts to playback changes
- Public player receives metadata and listener updates
- Config changes persist after restart

---

## Branch Strategy

- Use one branch per phase.
- Merge Phase 1 before starting Phase 2.
- Do not batch Phases 1-3 into one long-lived branch.
- Favor small reviewable commits inside each phase.

Suggested branch names:

- `refactor/race-safety`
- `refactor/sse-contracts`
- `refactor/config-store`
- `refactor/server-split`
- `refactor/frontend-runtime-alignment`
- `docs/relay-pipeline-clarification`

---

## Recommended Next Step

Start with **Phase 1: Concurrency and Correctness Baseline**.

That phase has the best payoff because it:

- addresses the only currently confirmed correctness failures
- reduces the chance that later refactors hide race regressions
- gives the rest of the roadmap a stable base

After Phase 1, move directly into Phase 2 so the admin/public event model becomes explicit before deeper server/frontend cleanup.
