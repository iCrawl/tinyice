# Runtime Direction

TinyIce's current runtime is audio-first and built around two layers:

- `relay.Relay` and `relay.Stream` are the live data plane for buffering, metadata, and listener fanout.
- `relay.RuntimeRegistry` is the control-plane layer for per-mount ownership metadata such as source kind, tenant association, and registered outputs.

What that means:

- Listener delivery and byte transport still target `Relay`.
- Source and output ownership should be registered through `RuntimeRegistry`.
- `Pipeline`, `Track`, `IngestSource`, `OutputAdapter`, and `PipelineManager` are retained for experiments and tests, but they are not the recommended runtime direction for the current server.

Current guidance for contributors:

- Reach for `RuntimeRegistry` when you need mount ownership or source/output bookkeeping.
- Reach for `Relay` and `Stream` when you need to change live audio transport behavior.
- Do not describe the current server as pipeline-based in docs or reviews.
