# Drop In-Process Self-Updater

Status: ready-for-agent

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Remove the in-process self-updater product surface from the integrated branch. Upgrades should be handled through upstream's package, container, or manual binary replacement paths. The server should no longer expose a runtime auto-update toggle or start any background self-update process.

## Acceptance criteria

- [ ] The runtime updater implementation, startup wiring, and CLI flag for self-updating are removed.
- [ ] Configuration defaults and settings APIs no longer expose update URL, checksum URL, or in-process auto-update behavior.
- [ ] Admin settings no longer show an auto-update toggle.
- [ ] Public docs and architecture references no longer claim in-process self-update support.
- [ ] Tests verify the removed surface is absent or replaced by supported package/container/manual upgrade documentation.

## Blocked by

- `docs/features/upstream-retention-integration/issues/01-upstream-first-integration-baseline.md`

