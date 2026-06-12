# Final Admin, Docs, And Regression Sweep

Status: ready-for-agent

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Complete the integration sweep across admin UI, documentation, and regression tests. The final integrated product should expose upstream admin surfaces and retained fork admin surfaces together, remove or revise stale documentation, rebuild generated assets, and provide focused regression coverage for the retention decisions.

## Acceptance criteria

- [ ] Admin UI exposes upstream webhooks, dashboard map/history/traffic, kiosk, player/video stats, and retained fork diagnostics/listener/stream controls without route loss.
- [ ] Docs treat stale upstream-rebase plans as historical and update active guidance for AutoDJ Gap Filler, Retained Dead Streams, Mount Ownership Registry, Metadata-Only Events, burst defaults, and updater removal.
- [ ] README and architecture docs no longer claim in-process self-update support.
- [ ] Generated frontend assets are rebuilt after source changes.
- [ ] Focused regression tests cover retained fork behavior and the main upstream behavior that fork code touches.
- [ ] Go and frontend checks for the integrated branch pass or document pre-existing failures.
- [ ] The retention audit and PRD remain accurate after implementation.

## Blocked by

- `docs/features/upstream-retention-integration/issues/02-drop-in-process-self-updater.md`
- `docs/features/upstream-retention-integration/issues/03-port-mount-ownership-registry.md`
- `docs/features/upstream-retention-integration/issues/04-retain-diagnostics-dead-streams-and-recovery.md`
- `docs/features/upstream-retention-integration/issues/05-port-autodj-gap-filler.md`
- `docs/features/upstream-retention-integration/issues/06-preserve-metadata-only-events.md`
- `docs/features/upstream-retention-integration/issues/07-restore-listener-management-caps-and-burst-visibility.md`

