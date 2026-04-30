# Plan: Upstream Rebase Integration

> Source PRD: `docs/prds/2026-04-29-upstream-rebase-integration.md`

## Architectural decisions

Durable decisions that apply across all phases:

- **Branching**: build a temporary integration branch from upstream `main`; keep the deployed `custom` branch untouched until verification passes.
- **Frontend ownership**: keep the Preact/Vite SPA and ShellRenderer model; do not restore server-rendered templates as the primary UI.
- **API ownership**: keep modular server APIs and route registration; port upstream API behavior into that structure.
- **Public media routes**: preserve existing public playback paths and add upstream-compatible HLS, master playlist, poster, and WHEP routes.
- **Stream API shape**: preserve fork-owned diagnostics, source classification, burst size, max listener, and listener management fields; add upstream video fields only as additive JSON fields.
- **Runtime model**: RuntimeRegistry remains the mount lifecycle ownership model; ListenerRegistry remains the operator listener-management model.
- **Diagnostics model**: mount-scoped diagnostics remain the source of truth for current state, reason, history, recovery, and manual versus automatic stop semantics.
- **Health policy**: auto-remove-dead-stream remains disabled by default.
- **Transcoding model**: use upstream pure-Go decoder/encoder internals as the base; reapply metadata mirroring, exponential retry backoff, and panic diagnostics.
- **ffmpeg boundary**: ffmpeg fallback is out of scope for this rebase and should be designed separately if needed later.
- **Security posture**: upstream auth/session/proxy/CSRF/SSRF/config/update hardening is mandatory integration content.
- **Generated assets**: resolve frontend source and package metadata first; regenerate dist assets from source after conflicts are resolved.
- **Documentation layout**: keep local `docs/prds` and `docs/plans` layout.
- **Release packaging**: keep upstream Docker/release workflow improvements, adjusted for fork-owned registry and image names.

## Current integration status

Status as of 2026-04-30:

- Implemented fork-owned runtime diagnostics, listener management, dead-stream recovery policy, AutoDJ song-command recovery, transcoder metadata mirroring, transcoder retry backoff, panic diagnostics, and updater checksum verification.
- Verified `go test ./...`.
- Verified `npm run build` from `server/frontend`; the existing HLS chunk-size warning remains.
- Verified `go build -o /tmp/tinyice-integration-check .`.
- Verified binary startup for 5 seconds with `/tmp/tinyice-integration-check -port 18080 -config /tmp/tinyice-integration-check.json`; ShellRenderer loaded 8 frontend entry pages and shutdown was graceful.
- Docker packaging remains unverified locally because the Docker daemon is not running in this environment.

---

## Phase 1: Safe Integration Baseline

**User stories**: 1, 13, 17, 18, 27, 28, 39

### What to build

Create the integration branch from upstream `main` and establish a clean baseline that compiles with upstream dependencies before fork-owned behavior is ported. Preserve local planning-doc layout and define the validation commands that every later phase must pass.

This slice is intentionally narrow: it creates the safe working surface for all later integration without changing production.

### Acceptance criteria

- [ ] A temporary integration branch exists from upstream `main`.
- [ ] The deployed `custom` branch is unchanged.
- [ ] Upstream dependency state is the starting point.
- [ ] Local PRD and plan layout remains under `docs/prds` and `docs/plans`.
- [ ] Baseline Go tests pass or any upstream-only failure is documented before porting fork changes.
- [ ] Frontend dependency/build requirements are documented for the integration branch.
- [ ] A running checklist exists for repeated `go test`, frontend build, and optional Docker build verification.

---

## Phase 2: SPA Shell And Route Preservation

**User stories**: 2, 24, 25, 26, 38

### What to build

Port the fork's SPA shell, modular route structure, setup guard behavior, and admin/public page ownership onto the upstream baseline. The demoable outcome is that the integration branch serves the existing SPA-based public/admin/login/setup experience rather than upstream templates, while retaining upstream-compatible route openings needed by later media phases.

This phase proves the fork's frontend/server architecture survived before deeper media and API behavior is layered in.

### Acceptance criteria

- [ ] Public, login, setup, admin, player, embed, explore, and developer pages are served by the SPA shell.
- [ ] Server-rendered templates are not required for normal runtime.
- [ ] Setup guard behavior still redirects non-setup paths during first-run setup.
- [ ] Existing static asset serving works after a frontend rebuild.
- [ ] Modular route registration is preserved.
- [ ] Go tests pass.
- [ ] Frontend production build passes.

---

## Phase 3: Stream Runtime And Operator Diagnostics

**User stories**: 5, 6, 7, 16, 23, 29, 30, 31, 32

### What to build

Restore the fork's runtime ownership and diagnostics path end to end: mount lifecycle ownership, listener management, `/api/streams` diagnostic fields, `/api/listeners` behavior, and AutoDJ recovery state should all be visible through the SPA/API after starting from upstream.

The demoable outcome is an operator can inspect streams, see current diagnostic reasons/history, manage listeners, and distinguish manual stops from runtime failures.

### Acceptance criteria

- [ ] Runtime ownership is recorded for Icecast, AutoDJ, relay pull, WebRTC, RTMP, SRT, and HLS output where applicable.
- [ ] Stream API responses include diagnostics, source classification, burst size, max listener data, enabled/visible state, and current-song state.
- [ ] Listener listing, disconnect, and move behavior works through authenticated API calls.
- [ ] Unsupported listener moves fail safely with a conflict-style response rather than corrupting stream state.
- [ ] Manual stop, automatic stop, degraded, dead, recovered, recovery started, recovery succeeded, and recovery failed states are represented in diagnostics.
- [ ] Dead streams are not auto-removed by default.
- [ ] Existing diagnostics, listener, runtime, and AutoDJ recovery regression tests pass or are updated to the integrated API shape.
- [ ] Go tests pass.

---

## Phase 4: Upstream Audio Media Correctness With Fork Transcoder Semantics

**User stories**: 3, 8, 12, 19, 20, 21, 22, 27, 28, 40

### What to build

Integrate upstream's pure-Go audio decode/transcode fixes while preserving fork-owned transcoder runtime behavior. The end-to-end path is a live or AutoDJ source feeding a transcoder output that uses upstream codec correctness and still mirrors metadata, backs off on repeated failures, and emits useful panic diagnostics.

The demoable outcome is that MP3, Ogg Opus, Ogg Vorbis, FLAC, FLAC-in-Ogg, and WAV-supported inputs follow upstream behavior, while a transcoded MP3 output keeps current-song metadata for ICY-capable clients.

### Acceptance criteria

- [ ] Upstream pure-Go decoder support is present for supported audio formats.
- [ ] Upstream Ogg/header/page alignment and resampling fixes are retained.
- [ ] Upstream MP3 bitrate and Opus encoder setting behavior is retained.
- [ ] Transcoder outputs mirror current-song metadata from input mounts at startup and on later changes.
- [ ] Stopping a transcoder stops metadata mirroring without duplicate updates.
- [ ] Repeated immediate transcoder failures use bounded exponential backoff.
- [ ] Panic recovery logs include enough diagnostic context for production debugging.
- [ ] ffmpeg is not introduced as part of this phase.
- [ ] Transcoder metadata, codec, and recovery tests pass.
- [ ] Go tests pass.

---

## Phase 5: Upstream Video, HLS, WHEP, And Player UX

**User stories**: 3, 9, 10, 11, 12, 23, 25, 38

### What to build

Port upstream's video ingest, HLS A/V, master playlist, poster, viewer metrics, WHEP gating, and player UX into the SPA architecture. The end-to-end path is OBS/RTMP or SRT video ingest through stream state, public HLS routes, stream API video fields, and SPA player behavior.

The demoable outcome is that a video mount plays through HLS by default, exposes stats/poster/viewer information, and only uses WHEP when explicitly gated.

### Acceptance criteria

- [ ] RTMP and SRT video ingest paths retain upstream media fixes.
- [ ] HLS A/V playlist and segment playback works for a video mount.
- [ ] Master playlist route works for configured variant groups.
- [ ] Poster route stores and serves per-mount poster data as intended.
- [ ] WHEP route exists but remains conservatively gated.
- [ ] Stream API responses include additive video fields without removing fork diagnostic fields.
- [ ] SPA player can distinguish audio-only and video-capable mounts.
- [ ] Existing audio-only playback still works.
- [ ] HLS route and stream API tests cover audio and video cases.
- [ ] Go tests and frontend production build pass.

---

## Phase 6: Security, Auth, Config, And Updater Hardening

**User stories**: 4, 17, 33, 34, 35

### What to build

Port upstream's production security hardening through the fork's API and SPA behavior. The end-to-end path includes login/session behavior, OIDC, mutating admin/API actions, remote URL validation, trusted proxy client IP handling, config writes, scan-ban behavior, and update verification.

The demoable outcome is that production security behavior matches upstream hardening while existing fork admin/API workflows continue to work.

### Acceptance criteria

- [ ] Session expiry, session reaping, login rotation, and session purge on user deletion work.
- [ ] OIDC state, nonce, ID-token verification, and verified-email handling are preserved.
- [ ] Mutating admin/API actions enforce CSRF expectations.
- [ ] Relay and webhook URL inputs reject unsafe local/private targets.
- [ ] Config writes are serialized to avoid concurrent write corruption.
- [ ] Trusted proxy handling uses forwarded client IPs only from trusted peers.
- [ ] Scan-ban behavior avoids banning normal repeated player requests to the same known/offline path.
- [ ] Updater downloads verify expected checksum before replacing the binary.
- [ ] Relevant auth, config, security, and updater tests pass.
- [ ] Go tests pass.

---

## Phase 7: Packaging, Documentation, And Generated Assets

**User stories**: 13, 14, 15, 17, 36, 37, 39

### What to build

Finalize release-facing artifacts after source integration is stable. Rebuild generated frontend assets from source, take upstream README content with any required fork registry adjustments, and integrate Docker/release workflow behavior using fork-owned image names.

The demoable outcome is a production artifact path: source builds the SPA, embeds the regenerated assets, builds the Go binary, and optionally builds a container image from the same integrated tree.

### Acceptance criteria

- [ ] Generated frontend assets are rebuilt from resolved source.
- [ ] README reflects upstream feature set and does not publish incorrect fork registry guidance.
- [ ] Docker build compiles frontend assets and Go binary from current source.
- [ ] Release workflow image names and tags are fork-appropriate.
- [ ] Dependency cleanup has been run and committed.
- [ ] Go tests pass.
- [ ] Frontend production build passes.
- [ ] Docker build passes if container deployment is used.

---

## Phase 8: Production Smoke Gate And Cutover Readiness

**User stories**: 1, 12, 16, 17, 29, 30, 31, 32

### What to build

Run the integrated branch through the production go/no-go gate and record the exact evidence needed before replacing the deployed branch. This phase does not add major features; it proves the integrated system is safe to deploy.

The demoable outcome is a reviewed integration branch with automated tests, frontend build, packaging verification, and manual smoke-test evidence for the production-critical paths.

### Acceptance criteria

- [ ] Full Go test suite passes.
- [ ] Frontend production build passes.
- [ ] Docker build passes if used for deployment.
- [ ] First-run setup, login, logout, and admin smoke tests pass.
- [ ] Icecast ingest smoke test passes.
- [ ] AutoDJ smoke test passes.
- [ ] At least one transcoder smoke test passes.
- [ ] Metadata propagation to transcoded MP3 output is verified.
- [ ] Listener connect, list, disconnect, and move behavior is verified.
- [ ] HLS audio playback is verified.
- [ ] HLS video playback is verified if video is enabled.
- [ ] WHEP remains gated and does not become default playback accidentally.
- [ ] Health degraded, dead, recovered, and AutoDJ recovery diagnostics are visible.
- [ ] Existing audio-only public playback remains stable.
- [ ] Manual smoke test steps and results are recorded before cutover.
