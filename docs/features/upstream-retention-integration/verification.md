# Integration verification

Verified on October 14, 2026. The final application source revision is `8f1b81adb7b13d5ea4d68674f4c2189580a32184`. Later documentation and delivery changes leave application code unchanged. The integration uses upstream `9bc4cad` and ports the accepted fork behavior from `d313d7e`.

Delivery uses `custom` with linear commits on top of upstream. The earlier ancestry merge and PR delivery plan are superseded by the user's direct-branch preference. Before changing delivery documentation, the reconstructed linear branch had exactly the same tree as the published integration at `bda91c6`. Local backup branches preserve the former `custom` and integration tips. GitHub now has pull requests disabled; attempts to close PR #2 returned that restriction, and its T3 tracking link was removed.

## Results

| Check | Result |
| --- | --- |
| Full `go test ./... -timeout=180s` | Passed at the final source revision. |
| Frontend typecheck, 21 behavior tests, production build | Passed. Rebuild preserves committed assets. |
| Embedded asset verification | All 25 asset bodies match rebuilt files. All 131 emitted import references resolve. |
| Windows amd64 and Linux arm64 builds with CGO disabled | Passed at the final source revision. |
| OpenAPI YAML and local references | Parsed; all 42 local references resolve. |
| Disposable compiled server baseline | Passed setup, session login, mount persistence, WAV AutoDJ, MP3 delivery, percent volume, and numeric pause/play. |
| Retained HTTP/SSE acceptance | Passed six checks at the final source revision. |
| Independent correctness and quality review | Passed after all accepted findings were repaired. |
| Focused race checks and vet | Passed for source retirement, metadata reconciliation, listener outage disconnect, configured AutoDJ edits, and activation assertions. |

Retained acceptance checks use actual HTTP audio, both SSE endpoints, authenticated handlers, and persisted configuration. They verify create-time flags, cap/burst persistence, real activation replay, distinct identities for repeated titles, pause health and resumed real progress, unlisted stream reconciliation, full-target move rejection, HTTP 202 followed by actual move completion, continued bytes after completion, disconnect accounting, and terminal clear replay.

The compiled browser at `9c800f7` additionally verified Add Mount flags, runtime-only AutoDJ cap/visibility edits, selected playlist loading, Studio pause/play/Next, an unlisted Player title change, and video-file publication. A real generated WebM file supplied audio and video through the file input. A separate Player decoded live audio and H.264 video at 320×180. After Stop and viewer closure, the source remained stopped, video disappeared, and the listener registry was empty. The later source change only repairs historical diagnostic precedence; its actual handler and negative-control tests pass independently.

Go audio tests verify persistent MP3 duration, tone pitch, requested bitrate, 22.05/48 kHz input conversion, partial EOF, pause behavior, bounded transition silence, and rejected stale producer writes. Additional tests cover recovery cancellation, detached ownership snapshots, tenant counts, HLS output lifetime, metadata mirror reconciliation, source replacement, and retained upstream security/transport contracts.

## Repaired findings

Independent review reproduced stale AutoDJ writes into a claimed successor, both-down fallback busy loops, lost activation time when metadata injection was disabled, outage disconnects that retained a listener slot, and ignored Opus page-flush errors. Each has a production regression. The Opus regression removes the mount exactly at the buffered publication boundary.

Frontend review reproduced stale titles after filtered/dropped metadata, ignored create-time flags, and delayed history responses populating another dialog. Versioned stream snapshots reconcile titles and clears. Create flags persist. Request generations guard history success, failure, and loading state, including reopening the same mount.

Runtime checks additionally found AutoDJ-only edits returning 404 and successful Studio actions returning empty bodies to a JSON client. Configured AutoDJ mounts now remain editable while stopped. Disabled jobs cannot resume through Studio/API/MPD before re-enable. Studio save/load/metadata actions return usable JSON.

The full suite exposed a historical-status interaction. Persisted shutdown state could override a newly claimed source without an in-memory diagnostic. Current ownership now takes precedence while recent history remains available. A predecessor negative control fails this assertion; the repaired normal and isolated race checks pass.

## Limits

The full race suite does not pass. The inherited Opus dependency fails Go `checkptr` in its translated encoder code. Existing upstream Opus roundtrip tests already exclude race instrumentation for that dependency; the new Opus publication test follows that convention. The broad server race run also exposes inherited logger fixture resets racing with GeoLookup background workers. A separate archive of the original fork reproduces that logger race. No race or checkptr instrumentation was disabled to obtain passing focused results.

An unlocked read in a retained metadata test was repaired with a literal expected title assertion. The repaired activation test passes isolated race runs. Broad-suite failures remain documented rather than reported as passes.

Camera/screen hardware, physical Safari/iOS playback, real remote RTMP/SRT sources, production load, and deployment were not exercised. Their upstream implementation and normal test coverage are retained. Browser media proof uses Chromium and a real generated file.

## Reproduce

Use the [verification skill](../../../.agents/skills/verify/SKILL.md) to launch a disposable instance. Run baseline smoke, then `python3 .agents/skills/verify/scripts/integration.py MANIFEST`. Drive the compiled pages using the feature maps and run cleanup. The helper stamps the source revision and records the binary hash, responses, audio prefix, acceptance results, and owned-process identity. Credentials remain in the private run directory; publish selected evidence only.

The [decision trail](../../plans/2026-10-14-upstream-retention-decisions.tsv) records design and repair decisions. The [lifecycle design](../../plans/2026-10-14-upstream-retention-design.txt) and [operator guide](../../upstream-integration.md) describe the implemented contracts.
