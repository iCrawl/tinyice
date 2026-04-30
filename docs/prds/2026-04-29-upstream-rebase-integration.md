# Upstream Rebase Integration PRD

## Problem Statement

The production TinyIce fork is deployed from a branch that has diverged substantially from upstream `main`.

The fork contains production-critical work around the Preact admin SPA, modular APIs, mount runtime ownership, listener management, stream diagnostics, AutoDJ recovery, and transcoder metadata mirroring. Upstream has continued shipping important media, video, HLS/WHEP, security, packaging, and operational fixes.

From the operator's perspective, neither option is acceptable:

- blindly taking upstream would risk regressing deployed admin/API behavior and production recovery tooling
- staying diverged would leave the fork without upstream media correctness, security hardening, video features, Docker packaging, and bug fixes

The integration must deliberately converge with upstream while preserving the fork's production architecture and operator-facing behavior.

## Solution

Create an intentional integration branch from upstream `main`, then port the fork-owned production features into that branch in reviewable subsystem commits.

The target product behavior is:

- upstream backend/media/security fixes are incorporated
- the fork keeps the Preact/Vite SPA, modular API files, and ShellRenderer frontend architecture
- the fork keeps RuntimeRegistry, listener registry, stream diagnostics, AutoDJ recovery, and transcoder metadata mirroring
- upstream video/HLS/WHEP/player capability is integrated into the SPA/API architecture rather than by restoring server templates
- generated frontend assets are rebuilt after source conflicts are resolved
- production deployment is blocked until tests and smoke checks prove audio-only behavior, diagnostics, recovery, metadata, and new HLS/video behavior all work

## User Stories

1. As a station operator, I want the fork to converge with upstream, so that production does not remain permanently isolated from important fixes.
2. As a station operator, I want the deployed Preact admin interface to remain intact, so that existing operational workflows do not regress.
3. As a station operator, I want upstream media correctness fixes, so that listeners receive stable audio and video streams.
4. As a station operator, I want upstream security fixes, so that production auth, sessions, CSRF, SSRF, proxy, and update behavior are hardened.
5. As a station operator, I want AutoDJ recovery behavior to survive the rebase, so that command-driven mounts can recover without manual intervention.
6. As a station operator, I want stream diagnostics to survive the rebase, so that dead, degraded, recovering, and stopped mounts explain their current state.
7. As a station operator, I want listener management to survive the rebase, so that listeners can be inspected, disconnected, or moved from the admin/API.
8. As a station operator, I want transcoded MP3 outputs to keep mirrored current-song metadata, so that third-party ICY-capable players display correct now-playing information.
9. As a station operator, I want upstream HLS video support integrated, so that OBS/RTMP/SRT video workflows can be supported without a separate fork.
10. As a station operator, I want WHEP playback gated conservatively, so that experimental WebRTC playback does not become the default production path.
11. As a station operator, I want HLS to remain the default browser video playback path, so that the safest supported path is used by default.
12. As a station operator, I want audio-only streams to remain unaffected, so that existing radio use cases continue working after video integration.
13. As a station operator, I want generated frontend assets rebuilt from source, so that hashed build artifacts are not manually merged incorrectly.
14. As a station operator, I want Docker packaging adjusted for the fork, so that container deploys publish and pull from the correct registry namespace.
15. As a station operator, I want upstream README content taken for now, so that documentation reflects the newest upstream feature set unless fork-specific differences require edits.
16. As a station operator, I want dead streams not to auto-disappear by default, so that diagnostics and recovery evidence remain visible.
17. As a station operator, I want production release gates before replacing the current branch, so that the integration is not deployed on faith.
18. As a developer, I want the integration split into subsystem commits, so that regressions can be reviewed and bisected.
19. As a developer, I want upstream transcoder internals to be the base, so that multi-codec decode, Ogg handling, resampling, and encoder settings are not reimplemented from stale fork code.
20. As a developer, I want the fork's transcoder metadata mirror reapplied on top of upstream, so that production metadata behavior is preserved.
21. As a developer, I want the fork's exponential transcoder retry logic preserved, so that bad inputs do not cause tight retry churn.
22. As a developer, I want panic stack diagnostics preserved, so that decoder and encoder failures are diagnosable in production logs.
23. As a developer, I want upstream video fields added to the existing stream API response, so that SPA consumers can adopt video behavior without losing diagnostic fields.
24. As a developer, I want the modular API split preserved, so that upstream logic lands in coherent API modules rather than a monolithic handler file.
25. As a developer, I want upstream public routes added to the modular route registration, so that master playlists, posters, WHEP, HLS playlists, and HLS segments have predictable precedence.
26. As a developer, I want the server-template architecture to remain removed, so that the fork has one frontend ownership model.
27. As a developer, I want upstream dependencies to be the base dependency set, so that media features compile without retaining unused legacy packages.
28. As a developer, I want `go mod tidy` after integration, so that dependencies reflect the final source tree.
29. As a developer, I want regression tests for fork-owned behavior, so that future upstream pulls do not silently remove production features.
30. As a developer, I want tests to cover public behavior rather than internal helper shapes, so that refactors remain possible.
31. As a support engineer, I want diagnostics and history to remain queryable through API responses, so that incidents can be investigated without log scraping.
32. As a support engineer, I want health recovery and manual stop semantics preserved, so that "operator stopped this" and "runtime failed this" remain distinct.
33. As a support engineer, I want upstream scan-ban false-positive fixes, so that normal player retries do not accidentally lock out real clients.
34. As a security operator, I want trusted proxy handling, so that bans, scan detection, and audit logs use real client IPs behind reverse proxies.
35. As a security operator, I want updater checksum verification, so that downloaded binaries are verified before replacing the running binary.
36. As a release operator, I want Docker build behavior to compile the SPA and Go binary together, so that the shipped artifact includes current frontend assets.
37. As a release operator, I want release workflow registry targets reviewed, so that fork releases do not publish to upstream-owned image paths.
38. As a product owner, I want upstream player UX changes integrated only when compatible with the SPA, so that new video/player features do not overwrite established admin behavior.
39. As a product owner, I want docs/prds and docs/plans to keep the fork's current layout, so that local planning history stays stable.
40. As a future maintainer, I want ffmpeg fallback kept out of this rebase, so that the integration does not introduce a new external runtime dependency during a high-risk convergence effort.

## Implementation Decisions

- The integration branch should start from upstream `main`.
- The current production `custom` branch should remain untouched until the integration branch passes review and verification.
- The integration should be split into reviewable subsystem commits rather than one large rebase-resolution commit.
- The fork's Preact/Vite SPA remains the frontend architecture.
- Server-rendered template files remain removed unless a specific handler cannot be migrated; the preferred answer is to rewrite the handler for the SPA.
- The modular API and route registration model remains the server architecture.
- Upstream `handlers_api_v2` logic should be ported into existing modular API modules, not kept as a monolithic file.
- RuntimeRegistry remains the source of mount lifecycle ownership metadata.
- ListenerRegistry remains the operator-facing listener management model.
- Stream diagnostics remain the operator-facing explanation model for current and recent mount state.
- Health and recovery semantics from the fork take precedence over upstream's simpler health monitor behavior.
- Auto-remove-dead-stream remains disabled by default.
- If auto-remove is introduced later, it should be explicit configuration and should emit diagnostics before removing a mount.
- Upstream media fixes should be ported as mandatory integration content.
- Upstream security fixes should be ported as mandatory integration content.
- Upstream Docker and release workflow changes should be kept, but adjusted for fork registry/image ownership.
- Upstream README content can be taken as-is initially.
- README registry references should be checked before release if Docker images are published under a fork namespace.
- The fork's docs/prds and docs/plans layout remains unchanged.
- Generated frontend assets are not source-of-truth for conflict resolution.
- Frontend sources, package metadata, and lockfiles should be resolved first; generated dist assets should be rebuilt afterward.
- Upstream video/HLS/WHEP support is in scope for this integration.
- HLS remains the default video playback path.
- WHEP remains gated conservatively, matching upstream's `?webrtc=1` caution.
- Upstream public HLS and video routes should be added to the modular public route registration.
- Master playlist, poster, and WHEP handlers should respect setup guard behavior and route precedence.
- Upstream video stream fields should be added as additive fields on the existing stream API response.
- Existing stream API diagnostic fields, source classification fields, burst size fields, and max listener fields must remain.
- Upstream transcoder internals should be the base for codec correctness.
- Fork-owned transcoder metadata mirroring should be reapplied on top of upstream's transcoder path.
- Fork-owned exponential transcoder retry backoff should be preserved.
- Fork-owned panic stack logging should be preserved.
- The final transcoder should still be pure-Go for this integration.
- No ffmpeg-based fallback or ffmpeg process management should be added in this rebase.
- A future ffmpeg fallback, if pursued, should be optional, explicitly configured, and treated as a separate decoder backend.
- The final dependency set should start from upstream, then retain only dependencies required by fork-owned behavior.
- The integration should end with dependency cleanup.

### Major Modules To Modify

- Frontend shell and SPA pages: integrate upstream player/video UX into the existing SPA without restoring templates.
- Route registration: add upstream HLS, master playlist, poster, WHEP, and player routes into the modular route model.
- Stream APIs: merge upstream video fields into the fork's richer stream diagnostics and source metadata response.
- Listener APIs: preserve listener list, disconnect, and move semantics.
- Runtime registry: preserve source/output ownership and attach upstream HLS/video lifecycle points where useful.
- Health and diagnostics: preserve mount-scoped diagnostic transitions, recovery semantics, and operator-visible history.
- AutoDJ/Streamer: preserve dead-stream recovery, song-command retry semantics, and manual versus automatic stop behavior.
- Transcoder manager: use upstream codec implementation as the base, then reapply metadata mirroring, retry backoff, and diagnostic logging.
- Relay stream data plane: integrate upstream buffer, Ogg, HLS, RTMP, SRT, MPEG-TS, frame, and video metric changes without dropping fork-owned listener/runtime hooks.
- Auth/session/config/security: port upstream hardening and keep fork config mutation safety.
- Packaging/release: port Docker and workflow changes with fork-appropriate registry targets.

### Deep Module Opportunities

- A stream response assembler should combine live relay snapshot, configured offline mounts, RuntimeRegistry ownership, diagnostics, listener counts, max listener configuration, and video metrics behind a stable API-facing shape.
- A transcoder lifecycle wrapper should own retry policy, metadata mirroring, panic recovery, and shutdown cleanup around the upstream codec loop.
- A public media route classifier should centralize path handling for HLS playlist, master playlist, segment, poster, WHEP, and fallback root behavior.
- A diagnostics adapter should translate health, AutoDJ, source, and recovery events into the common diagnostic model without spreading JSON shaping across handlers.

## Testing Decisions

- Good tests should validate externally observable behavior, not private implementation details.
- Tests should assert API payloads, HTTP status codes, stream state, listener behavior, metadata state, and route behavior.
- Tests should avoid coupling to exact helper function names, goroutine layout, or file split.
- Existing Go tests for relay, server, diagnostics, AutoDJ recovery, listener APIs, runtime registry, stream lifecycle, and transcoder metadata mirroring are prior art and should be retained or updated.
- Existing frontend build checks are required before production deployment.
- Stream API tests should prove existing diagnostic fields remain while upstream video fields are added.
- Stream API tests should cover active streams, configured offline mounts, access filtering, source classification, burst size, max listeners, diagnostics, and video metrics.
- Listener API tests should cover authentication, authorization, listing, disconnect, move, unsupported WebRTC move behavior, and target mount validation.
- Health diagnostics tests should cover degraded, dead, recovered, manual stop, recovery started, recovery succeeded, and recovery failed states.
- AutoDJ recovery tests should prove command-driven mounts recover after dead health events and manual stops do not auto-restart.
- Transcoder tests should prove metadata backfill, live metadata mirroring, stopped mirror cleanup, exponential retry behavior where practical, and panic recovery logging where practical.
- Transcoder codec tests should lean on upstream decoder/encoder behavior and only test fork-added behavior at the integration boundary.
- HLS route tests should prove playlist, master playlist, segment, poster, and WHEP routes dispatch correctly and preserve setup guard behavior.
- Player/frontend tests should focus on user-visible SPA behavior where existing test infrastructure supports it.
- Security tests should cover session expiry/reaper behavior, user-delete session purge, CSRF enforcement, SSRF guard behavior, trusted proxy client IP handling, and scan-ban false-positive protection.
- Config mutation tests should cover concurrent save safety where practical.
- Packaging verification should include a Docker build if production deploys containers.

### Production Go/No-Go Gate

- `go test ./...` passes.
- Frontend production build passes.
- Docker build passes if container deployment is used.
- First-run setup smoke test passes.
- Login/logout/admin smoke test passes.
- Icecast ingest smoke test passes.
- AutoDJ smoke test passes.
- At least one transcoder smoke test passes.
- Metadata propagation to a transcoded MP3 output is verified.
- Listener connect/list/disconnect/move behavior is verified.
- HLS audio playback is verified.
- HLS video playback is verified if video is enabled.
- WHEP remains gated and does not become default playback accidentally.
- Health degraded/dead/recovered diagnostics are visible in API/admin.
- AutoDJ dead-stream recovery behavior is verified.
- Existing audio-only public playback remains stable.
- Any manual smoke test steps are recorded in the integration PR or local release notes.

## Out of Scope

- Adding ffmpeg as a decoder, encoder, or ingest fallback.
- Replacing pure-Go transcoding with external process transcoding.
- Redesigning the admin UI beyond integrating upstream-compatible player/video features.
- Reintroducing server-rendered templates as the primary frontend.
- Moving local PRDs/plans into upstream's `docs/superpowers` layout.
- Publishing this PRD to an external issue tracker.
- Changing production auto-remove-dead-stream policy.
- Broad API versioning beyond additive fields needed for integration.
- Reworking multi-tenancy beyond preserving existing RuntimeRegistry and tenant usage behavior.
- Solving every possible arbitrary media input format.
- Performing the actual rebase implementation in this PRD step.

## Further Notes

- The latest inspected merge base was `1ff48d3`.
- At inspection time, the fork had 26 commits not in upstream and upstream had 40 commits not in the fork.
- A dry merge showed conflicts across relay media internals, server API/routing, frontend source/dist assets, and README.
- The conflict shape confirms this is an architectural integration, not a mechanical rebase.
- The current fork passed `go test ./...` and frontend `npm run build` before this PRD was written.
- Upstream passed `go test ./...` in a temporary worktree; upstream frontend build was not run there because dependencies were not installed in that worktree.
- The current fork does not contain an ffmpeg transcoder path. It uses pure-Go decoder/encoder libraries.
- Upstream also uses a broader pure-Go decoder path rather than ffmpeg.
- A future ffmpeg backend may still be valuable, but it should be introduced after upstream convergence as a separate design with explicit deployment and failure-mode decisions.
