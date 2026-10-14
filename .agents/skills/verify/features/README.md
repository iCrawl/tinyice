# TinyIce feature map

Read the map for the feature under test:

- [Streams and listeners](streams-listeners.md) covers mount editing, diagnostics, cap enforcement, and listener operations.
- [AutoDJ and Studio](autodj-studio.md) covers library, playlist, transport, volume, and recovery.
- [Player and metadata](player-metadata.md) covers public playback, event replay, activation, and clears.
- [Browser publishing](browser-publishing.md) covers browser microphone and video publishing.

The source baseline is upstream `9bc4cad`, with accepted integration contracts in `docs/features/upstream-retention-integration/PRD.md`. The compiled admin routes come from `server/frontend/src/pages/admin/AdminLayout.tsx`. API routes come from `server/server.go`. Added retained behavior must pass on the integrated build. The baseline helper deliberately makes no such claim.
