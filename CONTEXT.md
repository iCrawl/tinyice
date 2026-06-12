# TinyIce

TinyIce is an Icecast-compatible streaming server for live audio and video, AutoDJ playback, relays, transcodes, and browser-based administration.

## Language

**AutoDJ Gap Filler**:
The behavior that keeps an AutoDJ mount continuously decodable during normal between-track transitions by treating the gap as silence rather than as a disconnected or empty stream.
_Avoid_: silence frames, transition gap filler, silence mode

**Retained Dead Stream**:
An AutoDJ stream that has failed health checks but remains visible to operators so diagnostics and recovery can act on it.
_Avoid_: auto-removed dead stream, hidden failed stream

**Mount Ownership Registry**:
The control-plane record of which source owns a mount, which tenant it belongs to, and which runtime outputs are attached.
It is not the streaming data plane and does not own source-connection locking.
_Avoid_: pipeline manager, data-plane registry, source lock

**Metadata-Only Events**:
The public Server-Sent Events endpoint for external consumers that only need current-track changes and replay of the last known metadata.
The canonical path is `/events/metadata`; combined public stream events remain on `/events`.
_Avoid_: temporary metadata alias, internal-only metadata events
