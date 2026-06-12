# Upstream-First Integration Baseline

Status: ready-for-agent

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Establish the upstream-first integration baseline for the custom branch. The completed slice should use upstream `main` as the implementation base, keep upstream behavior for stream internals, transcodes, HLS/video transports, webhooks, security hardening, dashboard history, release packaging, and generated frontend assets, and leave clear integration points for retained fork behavior.

This slice should not re-port every fork feature. It should produce the stable base that later slices can build on without repeatedly resolving the same upstream conflicts.

## Acceptance criteria

- [ ] The integrated branch builds from upstream `main` as the behavioral baseline rather than restoring old fork implementations wholesale.
- [ ] Upstream stream source claiming, read/write deadlines, fanout behavior, Ogg/session handling, transcode internals, HLS/video transport fixes, webhooks, security checks, dashboard history, release packaging, and generated asset strategy are retained.
- [ ] Generated frontend assets are rebuilt from source after integration rather than manually merged.
- [ ] The integration leaves intentional extension points for AutoDJ Gap Filler, Retained Dead Streams, Mount Ownership Registry, diagnostics, listener management, Metadata-Only Events, per-mount caps, and burst visibility.
- [ ] Focused Go and frontend checks for the touched baseline pass or have documented pre-existing failures.

## Blocked by

None - can start immediately

