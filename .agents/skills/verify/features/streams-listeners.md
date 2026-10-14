# Streams and listeners

## Sub-features

Create a password-protected mount, edit visibility or enabled state independently, inspect a source, and disconnect listeners. Integration acceptance also requires diagnostics, per-mount caps, burst settings, individual listener kick, and truthful moves.

## How to get to it (user POV)

Log in and open `/admin/streams`. Use **Add Mount**. A row's `button[title="Edit mount"]` opens editing. Upstream also has **Kick source**, **Kick listeners**, and **Remove mount** buttons identified by their `title` attributes.

## Driving it with HTTP and T3 preview

The helper's `smoke` creates `/verify-mount` through `POST /api/streams` with `mount`, `password`, and `burst_size`. It sends `PUT /api/streams` with only `mount` and `visible`. Verify `/api/streams` and `data/tinyice.json` preserve enabled state and the configured burst. In the browser, inspect the same row and perform one edit through its modal, then capture the saved row and API response.

On the integrated build, require `status`, `status_class`, `status_reason`, `last_error`, `source_kind`, `source_label`, `burst_size`, `effective_burst_size`, and `max_listeners` for the appropriate mounts. A playing AutoDJ must report its source kind and running status without a remote IP. Failed AutoDJs stay listed. Require partial cap and burst edits to survive a reload. The default burst is 512 KiB and the clamp is 8 MiB.

Open real streaming HTTP connections to test caps. The effective cap is the smallest positive server and mount limit. Two mounts can each reach the server's per-mount ceiling. Count actual WebRTC playback separately from HLS metrics and exclude internal decoders.

The retained interface is `/admin/listeners`, `GET /api/listeners`, `POST /api/listeners/disconnect` with `{ "id": <listener-id> }`, and `POST /api/listeners/move` with `{ "id": <listener-id>, "target_mount": "/destination" }`. Confirm the integrated router before driving. Missing routes are acceptance failures. A successful move submission returns HTTP 202 with `id`, `status: "pending"`, `listener_id`, and `destination`. Keep the original `current_mount` until subscription succeeds. Require listener snapshots with `move_id`, `move_status`, and `move_error`. Verify completion using bytes from the destination, then disconnect and confirm released capacity. Full or disabled destinations must not claim success. Unsupported WebRTC moves return HTTP 409. Concurrent pending moves must reserve destination capacity and release it on failure, cancellation, or disconnect.

## Gotchas

Baseline upstream lacks retained listener management and diagnostics. **Kick listeners** disconnects all listeners on a mount and does not prove individual kick. An HTTP read that closes immediately cannot prove a pending move. Keep connection readers alive during listener journeys. A config-file comparison proves disk persistence only. Restart and log in again to prove reload behavior.
