# Drop In-Process Self-Updater

Status: needs-triage

Implementation: complete. Awaiting maintainer review. See [verification](../verification.md).

## Parent

`docs/features/upstream-retention-integration/PRD.md`

## What to build

Both pinned branches already lack a runtime self-updater, its configuration fields, and its CLI flag. Preserve that baseline and remove stale documentation claims. Upgrades use upstream's package, container, or manual binary replacement paths. HotSwap and restart controls retain upstream superadmin restrictions.

## Acceptance criteria

- [x] The runtime updater implementation, startup wiring, and CLI flag for self-updating remain absent.
- [x] Configuration defaults and settings APIs no longer expose update URL, checksum URL, or in-process auto-update behavior.
- [x] Admin settings no longer show an auto-update toggle.
- [x] Public docs and architecture references no longer claim in-process self-update support.
- [x] Tests verify the removed surface is absent or replaced by supported package/container/manual upgrade documentation.

## Blocked by

- `docs/features/upstream-retention-integration/issues/01-upstream-first-integration-baseline.md`

