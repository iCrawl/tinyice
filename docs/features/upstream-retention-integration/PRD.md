# Upstream Retention Integration PRD

Status: ready-for-agent

## Problem Statement

The custom branch has important fork behavior that operators and external consumers rely on, but upstream `main` has also gained a large set of better implementations for streaming reliability, transcodes, webhooks, HLS/video playback, dashboard history, packaging, and security hardening. The integration cannot be a simple "keep our branch" or "take upstream" decision.

From the operator's perspective, the risk is two-sided:

- keeping old fork implementations would throw away upstream fixes that are now safer and more complete;
- taking upstream wholesale would lose fork-owned behavior such as AutoDJ Gap Filler, Retained Dead Streams, Mount Ownership Registry metadata, operator diagnostics, live listener controls, and Metadata-Only Events.

The project needs an integration that uses upstream as the implementation base while deliberately re-porting only the fork behavior that still matters.

## Solution

Use upstream `main` as the integration base. Re-port fork behavior only when it provides an operator, listener, or external API outcome upstream does not provide. Prefer upstream implementations when they provide equal or better behavior.

The integrated product should:

- preserve AutoDJ continuity through AutoDJ Gap Filler;
- keep failed AutoDJ streams visible as Retained Dead Streams so diagnostics and recovery can act on them;
- preserve dead `song_command` recovery;
- preserve mount-scoped diagnostics and recent diagnostic history;
- preserve the Mount Ownership Registry as a narrow control-plane ownership model;
- preserve live listener management and per-mount listener caps;
- preserve configured/effective burst visibility while using upstream's larger default listener burst;
- preserve Metadata-Only Events as an external API at `/events/metadata`;
- keep upstream stream, transcode, HLS/video, webhook, dashboard, release, and security improvements;
- drop the in-process self-updater.

## User Stories

1. As a radio operator, I want the upstream integration to preserve required fork behavior, so that an update does not silently remove production workflows.
2. As a radio operator, I want upstream stream reliability fixes, so that source reconnects and listener fanout are less likely to hang or corrupt audio.
3. As a radio operator, I want upstream security hardening, so that the fork does not reintroduce fixed auth, CSRF, URL validation, or session risks.
4. As a radio operator, I want upstream release and packaging improvements, so that upgrades can use package, container, or manual binary replacement paths.
5. As a radio operator, I want the in-process self-updater removed, so that the server does not overwrite its own binary at runtime.
6. As an AutoDJ listener, I want AutoDJ playback to remain decodable during normal between-track gaps, so that my browser player does not fail when joining during a transition.
7. As an AutoDJ listener, I want AutoDJ Gap Filler to avoid publishing fake track metadata, so that the now-playing display only reflects real tracks.
8. As an AutoDJ listener, I want manual stop or pause to remain intentional silence or shutdown, so that AutoDJ Gap Filler does not mask operator control.
9. As a radio operator, I want AutoDJ output continuity to be implemented on upstream encoder/session internals, so that the fork keeps upstream decoder and stream fixes.
10. As a radio operator, I want a failed AutoDJ stream to remain visible as a Retained Dead Stream, so that I can inspect what failed.
11. As a radio operator, I want failed AutoDJ streams to keep diagnostic history, so that I can see whether the failure was a source disconnect, empty command output, invalid file, or recovery failure.
12. As a radio operator, I want dead `song_command` recovery to continue, so that transient command failures can recover without manual intervention.
13. As a radio operator, I want dead `song_command` recovery workers to cancel on stop, delete, and shutdown, so that recovery does not resurrect streams I intentionally stopped.
14. As a radio operator, I want upstream AutoDJ `on_play_command` and webhook behavior to remain, so that retaining recovery does not regress upstream integrations.
15. As a radio operator, I want mount diagnostics in stream APIs, so that admin UI can show status, status class, reason, last error, and recent history.
16. As a radio operator, I want the admin streams view to show diagnostic state, so that I can distinguish running, degraded, recovering, dead, stopped, and error states.
17. As a radio operator, I want the Mount Ownership Registry to identify source ownership, so that AutoDJ, relay, WebRTC, RTMP, SRT, and Icecast sources are labeled correctly.
18. As a tenant administrator, I want tenant live stream counts to derive from attached mount ownership, so that tenant usage is accurate.
19. As a radio operator, I want HLS output lifecycle metadata retained in the Mount Ownership Registry, so that runtime output attachment remains observable.
20. As an implementer, I want Mount Ownership Registry to stay out of source-connection locking, so that upstream source claim logic remains authoritative.
21. As a radio operator, I want live listeners listed in the admin UI, so that I can see who is connected now.
22. As a radio operator, I want to disconnect an individual listener, so that I can resolve abusive or stuck clients without restarting a stream.
23. As a radio operator, I want to move HTTP listeners between mounts, so that I can shift listeners during live operations.
24. As a radio operator, I want WebRTC listener move conflicts handled clearly, so that unsupported moves do not look successful.
25. As a radio operator, I want per-mount listener caps, so that important mounts can have stricter capacity limits than the global default.
26. As a listener, I want listener caps enforced consistently, so that overloaded mounts reject excess listeners predictably.
27. As a radio operator, I want configured and effective burst size shown in API/admin surfaces, so that I can tune listener startup behavior.
28. As a listener, I want upstream's larger default listener burst, so that short network stalls are less likely to force reconnects.
29. As a radio operator, I want per-mount burst overrides, so that mounts with special latency or metadata needs can be tuned.
30. As an external metadata consumer, I want `/events/metadata`, so that I can subscribe only to current-track changes without parsing stream stats.
31. As an external metadata consumer, I want last-known metadata replay on connect, so that my display has immediate now-playing state.
32. As an external metadata consumer, I want Metadata-Only Events filtered to visible mounts, so that unlisted streams do not leak through the public endpoint.
33. As a public player user, I want combined `/events` stream and metadata events to keep working, so that the player can update stats and title from one connection.
34. As a radio operator, I want upstream transcode metadata mirroring behavior, so that transcoded outputs mirror song, genre, URL, description, public flag, and visibility.
35. As a radio operator, I want fork metadata notifications preserved around upstream transcode mirroring, so that external Metadata-Only Events still fire for transcoded outputs.
36. As a radio operator, I want upstream HLS/video/RTMP/SRT/WebRTC fixes retained, so that mobile, iOS, video, and source-flap behavior does not regress.
37. As a radio operator, I want upstream webhooks retained, so that templated webhook bodies, presets, `now_playing`, and AutoDJ track-start hooks continue to work.
38. As a radio operator, I want upstream dashboard map, kiosk, listener history, and traffic totals retained, so that operational visibility improves rather than regresses.
39. As a radio operator, I want fork diagnostics added on top of upstream history storage, so that the project does not lose upstream history reliability work.
40. As an admin UI user, I want upstream pages and fork pages to coexist, so that webhooks, kiosk, dashboard, diagnostics, streams, and listeners are all available.
41. As an implementer, I want generated frontend assets rebuilt after source integration, so that conflicts in generated files do not drive behavior.
42. As an implementer, I want stale upstream-rebase docs identified as historical, so that agents do not follow old retention claims.
43. As an implementer, I want updater-related docs removed or revised, so that the public docs do not claim unsupported self-update behavior.
44. As an implementer, I want focused regression tests for retained fork behavior, so that future upstream syncs can distinguish deliberate retention from stale code.
45. As an implementer, I want upstream behavior smoke tests where fork code touches shared paths, so that re-porting fork features does not erase upstream fixes.

## Implementation Decisions

- Upstream `main` is the implementation base for the integration.
- Fork behavior is retained only when upstream lacks the behavior or when the fork has an explicit product policy.
- AutoDJ Gap Filler is retained and ported onto upstream's current decoder, encoder, and stream session internals.
- AutoDJ Gap Filler must emit generated silent PCM only for normal AutoDJ between-track transitions.
- AutoDJ Gap Filler must not emit metadata for generated silence.
- Manual AutoDJ stop, pause, and delete remain explicit operator actions and must not be masked by endless generated silence.
- Retained Dead Streams are a required fork policy.
- Upstream's default health auto-remove behavior must not remove failed AutoDJ streams by default in the fork.
- If auto-remove is ever exposed later, upstream safeguards for transient transcoded outputs should still be retained.
- Dead `song_command` recovery is retained.
- Dead `song_command` recovery must integrate with upstream AutoDJ hooks and webhooks rather than replacing them.
- Mount diagnostics are retained as operator-facing status and recent history.
- Diagnostics must extend upstream history storage instead of replacing upstream listener history, traffic totals, geo, or dashboard history behavior.
- RuntimeRegistry remains as the Mount Ownership Registry.
- The Mount Ownership Registry records source kind, tenant ownership, attached runtime outputs, and timestamps.
- The Mount Ownership Registry does not own source-connection locking; upstream source claim and release behavior owns that.
- Mount Ownership Registry reads should be snapshot-based, not direct mutable pointer exposure.
- Live listener management is retained.
- Per-mount listener caps are retained and should be persisted through create/update APIs.
- Upstream's larger listener burst default is accepted.
- Per-mount burst overrides, configured burst size, and effective burst size remain visible in API/admin surfaces.
- Metadata-Only Events are retained as an external public API.
- `/events/metadata` is the canonical metadata-only endpoint and must replay last-known metadata for visible mounts.
- `/events` remains the combined public stream event endpoint and should also emit metadata events for public clients.
- Upstream public stream/video fields must not be lost while adding metadata replay behavior.
- Upstream transcoder internals are preferred over the fork implementation.
- Upstream transcode metadata mirroring is preferred over the fork mirror loop.
- Fork-specific metadata notification semantics must be added around upstream transcode mirroring where needed.
- Upstream HLS, video, RTMP, SRT, and WebRTC transport fixes are preferred.
- Upstream webhook APIs, webhook admin UI, `on_play_command`, and `now_playing` are preferred.
- Upstream auth, CSRF, trusted URL, session, config-save, and role hardening are preferred.
- Upstream dashboard map, kiosk, listener history, and traffic totals are preferred.
- The in-process self-updater is dropped.
- Updater config fields, CLI flag, settings UI, docs, and tests should be removed during integration.
- Generated frontend assets should be regenerated after source integration rather than hand-merged.
- Stale April upstream integration docs should be treated as historical context, not current implementation authority.

## Testing Decisions

- Tests should focus on externally observable behavior rather than internal helper structure.
- Existing server API handler tests are the preferred seam for stream list fields, diagnostics, source labels, burst visibility, listener caps, Metadata-Only Events, and updater removal.
- Existing relay behavior tests are the preferred seam for AutoDJ Gap Filler, transcode metadata mirroring behavior, listener registry behavior, Mount Ownership Registry tenant counts, and source lifecycle events.
- Existing stream/listener handler tests are the preferred seam for listener burst defaults, per-mount burst overrides, per-mount listener caps, and stale source disconnect behavior.
- Existing AutoDJ tests are the preferred seam for gap filler metadata timing, decoder shutdown, recovery cancellation, and `song_command` recovery.
- Existing HLS/video/RTMP/SRT/WebRTC tests should be preserved and extended only where fork metadata/runtime ownership touches upstream lifecycle.
- Existing frontend source tests are the preferred seam for admin streams diagnostics, listeners management, settings updater removal, and route visibility.
- Public SSE tests should verify both `/events` metadata events and `/events/metadata` replay semantics.
- Regression tests should explicitly prove that generated silence does not publish metadata and that metadata is published only after the first real track frame is active.
- Regression tests should prove that a runtime-owned AutoDJ mount can be shown as running without a remote source IP.
- Regression tests should prove that dead streams remain visible and recoverable rather than auto-removed by default.
- Regression tests should prove that updater settings cannot enable an in-process updater because that product surface no longer exists.
- Build verification should include Go tests for touched packages and frontend test/build checks for touched admin/public surfaces.

## Out of Scope

- Reintroducing the experimental pipeline manager as the active data plane.
- Replacing upstream source claim and release logic with Mount Ownership Registry locking.
- Designing a new updater, signed-update protocol, or package manager.
- Reworking multi-tenancy beyond preserving existing tenant live stream ownership behavior.
- Rewriting the whole admin UI design system.
- Replacing upstream webhooks with the old fork webhook model.
- Hand-merging generated frontend assets.
- Retaining stale docs that contradict the retention audit as active guidance.

## Further Notes

The retention audit for this PRD is `docs/plans/2026-06-12-upstream-retention-audit.md`. That audit closes the major product decisions:

- keep AutoDJ Gap Filler;
- keep Retained Dead Streams;
- keep the Mount Ownership Registry as a narrow ownership registry;
- keep Metadata-Only Events for external consumers;
- use upstream's larger default listener burst;
- drop the in-process self-updater;
- use upstream implementations for stream internals, transcodes, HLS/video transports, webhooks, security, dashboard history, packaging, and generated assets.

