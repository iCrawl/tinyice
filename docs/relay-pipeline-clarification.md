# Relay vs Pipeline

TinyIce's production runtime is still built around `relay.Relay` and `relay.Stream`.

What that means:

- Listener delivery, source ingestion, metadata updates, and most server wiring still target `Relay`.
- `Pipeline`, `Track`, `IngestSource`, `OutputAdapter`, and `PipelineManager` are retained as internal groundwork for future multi-track, tenant-aware, or alternate-output work.
- `PipelineManager` should be treated as an adapter around `Relay`, not as the current runtime owner.

Current guidance for contributors:

- Reach for `Relay` and `Stream` first when changing live runtime behavior.
- Treat pipeline code as isolated groundwork unless a task explicitly targets tenant pipeline evolution.
- Do not describe the current server as "pipeline-based" in docs or reviews.
