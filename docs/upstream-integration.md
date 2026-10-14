# Upstream integration

This integration starts at upstream `main` commit `9bc4cad13dd36cdda27ef0869a52e66479b65d33`. The prior `custom` branch was `d313d7e06e168f87f1afed1c381ec4c2712b4cc6`. Their common base was `d847446576001d07c65323b51d6c4e4198c1a144`. At those pins, upstream had 98 exclusive commits and the fork had 13.

The [integration PRD](features/upstream-retention-integration/PRD.md) and [lifecycle design](plans/2026-10-14-upstream-retention-design.txt) define current behavior. Historical rebase and pipeline plans describe earlier proposals. They do not override this implementation contract.

## Operator behavior

AutoDJ Gap Filler keeps a persistent encoder alive during normal transitions after real playback. Generated silence lasts at most ten seconds. Startup, exhausted selection, failures, recovery, pause, and stop do not generate indefinite audio. Real producer progress is tracked separately from output availability.

Failed streams retain diagnostics. Recovery has one playback owner, starts with a one-second delay, and increases to at most one minute. Streams expose attempt count, next retry, and last error. Pause, stop, deletion, and shutdown revoke obsolete work. A disconnected source remains stopped rather than becoming a stalled active producer.

Mount edits preserve configured listener limits and burst sizes. Effective limits use the smallest positive per-mount limit and global per-mount ceiling. Zero contributes no bound. Default listener burst is 512 KiB and configured bursts are clamped to 8 MiB. Managed HTTP and WebRTC connections are counted separately from HLS viewer metrics.

HTTP listener moves return HTTP 202 with a pending move ID. The listener's current mount changes when its subscription changes. Completed or failed state is visible in listener snapshots. Full destinations reject the request with HTTP 409. Disconnects and failed transfers release reserved capacity. WebRTC moves require disconnect and return HTTP 409.

## Metadata consumers

`/events/metadata` replays current metadata for visible mounts. `/events` retains combined public stream events and metadata events. Its named stream snapshots also reconcile playback state for an unlisted Player whose mount is already known.

Metadata retains `mount`, `title`, `artist`, and RFC3339 `started_at`. It adds string `playback_id` and numeric `revision`. A real activation starts at the first successfully published frame. Repeated titles get distinct identities. Pause preserves identity. Terminal completion or removal publishes an empty title and identity with a newer revision. Replay retains that clear.

Revisions are allocated by the Relay across Stream recreation. Each Stream owns its current playback snapshot. A transcoder output retains input playback identity and activation time while publishing its own revision. One mirror per transcoder uses event delivery plus five-second reconciliation for dropped changes and input replacement.

## Runtime ownership

Stream source claims bind writers and cleanup to a generation. An obsolete producer cannot detach or write into its successor. The Mount Ownership Registry returns detached source and output snapshots. It records tenant ownership and HLS output lifetimes independently of source claims.

Upstream security, decoder, transport, webhooks, dashboard history, and packaging remain the base. Go Live retains microphone, camera, screen, and video-file modes. Player retains HLS and WHEP fallback. Studio uses numeric AutoDJ state, numeric playlist IDs, flat events, `playlist_version`, percent volume, `is_dir`, and explicit selected playlist paths.

There is no in-process self-updater in either pinned branch. Use package, container, or manual binary replacement for upgrades. Existing HotSwap and restart controls retain upstream authorization.

## Verification

Use the [project verification skill](../.agents/skills/verify/SKILL.md) to launch a disposable instance, run baseline smoke, run retained HTTP/SSE acceptance, drive compiled browser pages, and stop only its owned process. The helper records the binary revision and hash and keeps evidence after cleanup. Go behavior tests additionally exercise encoder content, source replacement, fallback, recovery cancellation, and metadata mirroring.
