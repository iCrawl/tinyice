# Upstream Retention Audit - 2026-06-12

## Scope

Current fork branch: `custom` at `a0754f8`.
Upstream baseline: `upstream/main` at `d847446`.
Merge base: `9cc24ef`.

At this point upstream has 108 commits not in the fork, and the fork has 11 commits not in upstream. The integration should use upstream as the base and re-port fork behavior only where it still produces an operator or listener outcome upstream does not provide.

## Retention Rule

Prefer upstream implementation when it provides the same or better outcome. Keep fork behavior only when upstream lacks the behavior, or when the fork behavior encodes a product policy we still want. When a fork behavior is retained, port it onto upstream's current internals instead of restoring old files wholesale.

## Decided Policies

- Dead streams remain visible and recoverable by default. Do not adopt upstream's default auto-remove after the grace period as the fork default.
- If auto-remove is exposed or enabled later, retain upstream's safeguards that avoid removing transient transcoded outputs during source flaps.
- RuntimeRegistry remains as a narrow Mount Ownership Registry. It records source kind, tenant ownership, and attached runtime outputs; it does not replace upstream source-claim locking or become a general pipeline framework.
- Metadata-only SSE is an external API. Keep `/events/metadata` as the public metadata-only endpoint with last-known metadata replay, while `/events` remains the combined public stream event endpoint.
- Use upstream's larger default listener burst size. Keep fork-owned per-mount burst configuration, `burst_size`/`effective_burst_size` API fields, and admin visibility.
- Drop the in-process self-updater. Upgrades should use upstream's package, container, or manual binary replacement paths.

## Required Fork Behavior To Keep Or Adapt

| Area | Decision | Why | Porting notes |
| --- | --- | --- | --- |
| AutoDJ Gap Filler | Keep/adapt | Upstream does not provide a persistent AutoDJ output session that emits generated silent PCM during normal between-track gaps. The fork's behavior keeps fresh listeners decodable during AutoDJ transitions instead of returning a tiny or undecodable initial response. | Re-port onto upstream's newer decoder/encoder path. Preserve "no silence metadata" and "manual stop does not emit endless silence" behavior. |
| Retained dead streams | Keep/adapt | Upstream auto-removes dead streams by default after a grace period. The fork policy is that failed AutoDJ streams remain visible so operators can inspect diagnostics and recovery can act on them. | Disable default auto-remove for this path. Preserve upstream's transcode-output auto-remove safeguards if auto-remove is later exposed as an optional mode. |
| Dead `song_command` recovery | Keep/adapt | Upstream supports `song_command`, `on_play_command`, and webhooks, but does not have the fork's health-driven recovery loop for dead AutoDJ command mounts. | Integrate with upstream's `on_play_command` and track-start hooks. Keep recovery cancellation on stop/delete/shutdown. |
| Mount diagnostics | Keep/adapt | Upstream has health state and history improvements, but not the fork's operator-facing diagnostic event model with status class, actor, reason, last error, and recent history. Diagnostics are part of the retained-dead-stream policy. | Add diagnostic events on top of upstream's HistoryManager improvements instead of replacing upstream history storage. |
| Listener registry and live listener admin | Keep/adapt | Upstream has listener history, geo, and metrics, but not live per-listener list/disconnect/move operations. | Port registry hooks around upstream listener write deadlines and stream fanout fixes. Keep WebRTC move conflict handling. |
| Per-mount listener caps | Keep/adapt | Upstream only has a global listener limit. The fork adds mount-specific caps and effective limit reporting. | Keep if live listener controls remain a requirement. Fix API create/update persistence for `max_listeners`. |
| Per-mount burst visibility | Keep/adapt | Upstream has per-mount burst behavior, but not the fork's stream API/admin visibility for configured and effective burst values. | Use upstream's 512 KiB default. Keep per-mount overrides plus `burst_size` and `effective_burst_size` reporting. |
| Mount Ownership Registry | Keep/adapt | Upstream does not expose the fork's source ownership vocabulary or HLS output attachment tracking. The fork uses this for source labels, running status without a remote source IP, tenant live stream counts, and output lifecycle metadata. | Port as metadata around upstream source/output lifecycle. Tighten the API so callers attach source ownership with explicit stream/source/tenant data and read immutable snapshots. Do not use it as source-connection locking. |
| Metadata-only events and replay | Keep/adapt | External consumers need a metadata-only SSE endpoint. The fork also has explicit metadata subscription behavior used by AutoDJ gap filler tests, including no metadata during silence and metadata after the first track frame. | Keep `/events/metadata` as an external API and preserve last-known metadata replay. Also emit metadata events on `/events` for combined public clients. Port this onto upstream's public event handler without losing upstream stream/video fields. |
| Admin diagnostics and listener UI | Keep/adapt | Upstream admin UI does not include the fork's live listener management or diagnostic stream state surfaces. | Add these pages/features to upstream's current admin shell without dropping upstream webhooks, kiosk, map, traffic, and toast work. |

## Upstream Behavior To Prefer

| Area | Decision | Why | Porting notes |
| --- | --- | --- | --- |
| Stream source ownership and stale disconnects | Use upstream | Upstream's `TryClaimSource`/`ReleaseSource`, source read deadlines, listener write deadlines, Ogg freshness gate, and fanout fixes are stronger than the fork's older source token model. | Re-add diagnostics/runtime events around upstream claims instead of restoring the fork's stream internals. |
| Transcoder internals | Use upstream | Upstream has shared decoder hubs, chained Ogg/libopus decode, session flushing, auto MP3 transcodes, transcoder visibility, and better metadata mirroring. | Re-add only fork-specific retry/backoff or panic diagnostics if still useful. |
| Transcoder metadata mirroring | Drop fork implementation | Upstream now mirrors more fields and fixes the fork implementation's goroutine leak risk by tying the mirror loop to the transcode context. | Keep behavior-level tests if useful, but use upstream code. |
| HLS, video, RTMP, SRT, and WebRTC transport fixes | Use upstream | Upstream contains major compatibility and resilience fixes for iOS HLS, range handling, source flaps, RTMP/H264, SRT, and WebRTC. | Add runtime registry labels around upstream lifecycle hooks only. |
| Webhooks and AutoDJ hooks | Use upstream | Upstream has the newer webhook model, webhooks admin page, `on_play_command`, and `now_playing` callback. | Port recovery and gap filler without regressing upstream webhook behavior. |
| Security and auth hardening | Use upstream | Upstream added CSRF, role, URL validation, timing oracle, session, and config-save hardening that the fork should not overwrite. | Avoid copying old fork handlers over upstream handlers. |
| History, geo, kiosk, and dashboard traffic | Use upstream | Upstream has listener history ranges, geo map, kiosk, traffic totals, and HistoryManager reliability work. | Add diagnostics as an extension instead of replacing history/dashboard code. |
| Release packaging and security docs | Use upstream | Upstream added nFPM packaging, release workflow updates, Dependabot, `SECURITY.md`, and `CHANGELOG.md`. | Adjust naming/registry values for the fork only where needed. |
| Generated frontend assets | Regenerate | `server/frontend/dist/**` is generated and conflict-prone. | Merge source, then rebuild assets. Do not hand-merge generated files. |
| In-process updater | Drop | Upstream intentionally removed the self-updater in favor of package/manual/container updates. The fork's checksum verification is better than a blind updater, but the product surface is still risky and no current deployment requirement was retained. | Remove the updater package, `-autoupdate` flag, update config defaults, Settings toggle, README claims, and tests during integration. |

## Decisions Still Needed

No retention decisions remain open from this audit.

## Stale Or Superseded Fork Docs

The April upstream integration docs should be treated as historical context, not current authority. Several items they listed as fork-owned are now present upstream or have better upstream implementations.

| Doc | Current status |
| --- | --- |
| `docs/plans/2026-04-29-upstream-rebase-integration.md` | Stale. It predates upstream webhooks, metadata mirroring, decoder hub, dashboard, HLS, and security work. |
| `docs/prds/2026-04-29-upstream-rebase-integration.md` | Stale for the same reason. Keep only as history unless rewritten around this audit. |
| `docs/prds/2026-04-09-autodj-ogg-metadata-propagation-to-mp3-transcodes-design.md` | Superseded by upstream's broader transcode metadata mirroring. |
| README and architecture references to auto-update | Stale. Remove claims about in-process auto-update, update URL/checksum settings, and updater package ownership. |
| AutoDJ gap filler docs | Still relevant. Update terminology to AutoDJ Gap Filler and porting notes to upstream internals. |
| Dead AutoDJ recovery docs/tests | Still relevant. Health-driven recovery and retained dead streams are required fork policy. |
| Listener management and diagnostics docs/tests | Still relevant if operator live-control and diagnostic history remain required surfaces. |
