---
name: verify
description: Launch and drive disposable TinyIce instances through the compiled admin UI and streaming APIs, with evidence and safe cleanup. Use for TinyIce behavior verification and integration acceptance.
---

# Verify TinyIce

Use the compiled admin UI as the primary interface. Public HTTP audio, metadata events, and WebRTC are additional interfaces. Read the relevant [feature map](features/README.md) before driving a journey.

## Launch

From the repository root, run the executable helper:

```sh
.agents/skills/verify/scripts/verify.py launch
```

Use `--go /path/to/go` when Go 1.26 is outside `PATH`. Use `--binary /path/to/tinyice` to inspect an existing build. The helper builds with `go build`, stamps `main.Commit` with the checkout revision, and uses the checked-in embedded `server/frontend/dist` assets. It records `--version`, Git revision, working tree state, and binary hash. Rebuild frontend assets through the repository's `make generate` before verification when frontend source changed. Never substitute handwritten HTML for compiled assets.

The helper prints an absolute `run.json` path. Use that path as `MANIFEST` below. It allocates a free loopback port and a private evidence directory. `--port` chooses a port and `--evidence-dir` chooses the parent directory. The server runs with its own working directory, config, PID file, history database, and playlists.

Launch waits for the generated setup token in its private log, posts to `/setup/verify-token` and `/setup/complete`, and logs in through `/login`. Readiness requires a superadmin page with a CSRF token, served embedded assets, and an authenticated `/api/streams` response. A listening socket alone is insufficient. Scratch login details are in `credentials.json` beside the manifest. Do not print tokens, cookies, password hashes, or source passwords in reports.

## Doctor

```sh
.agents/skills/verify/scripts/verify.py doctor "$MANIFEST"
```

This read-only check verifies PID start identity, executable, binary hash and version, port ownership, admin session, and stream API access. It does not rewrite evidence or config. Run it when the instance looks wrong. It requires Linux `/proc`, Python 3, and the built binary. A restarted server needs a new login because sessions live in memory.

## Drive

```sh
.agents/skills/verify/scripts/verify.py smoke "$MANIFEST"
```

Run smoke once per fresh launch. It exercises mount creation, partial visibility editing, config persistence, WAV AutoDJ creation, library and playlist reads, real MP3 delivery, one-percent volume, and numeric pause/play state. It seeds two 30-second stereo WAV tones through Python's `wave` module. No ffmpeg is required. This is baseline smoke. `smoke.json` explicitly records that retained integration features are unverified.

For browser work, call native T3 `preview_status`, then `preview_open` at the manifest's URL with `reuseExistingTab=false`. Use the returned `tabId` on every later tool call. Log in with `input[name=username]`, `input[name=password]`, and `button[type=submit]`. Inspect with `preview_snapshot` before interaction. Prefer ARIA labels and exact button text from the feature map. Use standalone browser automation only if native tools are absent, unavailable, or fail twice.

After baseline smoke, run the retained HTTP and SSE checks once:

```sh
python3 .agents/skills/verify/scripts/integration.py "$MANIFEST"
```

This creates disposable acceptance mounts and records `integration.json`. It checks persisted create flags, listener limits, full-destination rejection, accepted move completion, disconnect accounting, real playback identities, pause health, unlisted stream reconciliation, and terminal clear replay. It consumes actual HTTP audio and both event endpoints. Browser media, encoder pitch, bounded gaps, fallback, and recovery cancellation need the feature journeys or Go behavior checks.

Use public API routes and ordinary cookie sessions for additional journeys. Form endpoints need the session's `X-CSRF-Token` or `csrf` field. JSON `/api/` requests use `Content-Type: application/json`, matching the shipped UI's CSRF rule. Never use internal setters or test-only endpoints as proof.

## Evidence

The manifest's `run_dir` holds `build.log`, `server.log`, `responses.jsonl`, `ready.json`, `smoke.json`, `audio-prefix.mp3`, and the disposable `data` directory. Responses record the action route, time, status, and resulting body. Asset responses record byte counts and hashes. Authentication responses are redacted. The private directory still contains scratch tokens and cookies, so publish selected evidence only.

Capture browser screenshots through `preview_snapshot` with `save=true`. Copy the returned screenshot into the run directory and record its path in a browser evidence JSON file. Capture both the action and the observed result. Compare the written config for persisted settings. Stream bytes prove delivery, but tone decoding and bitrate analysis require a decoder and remain separate checks. A screenshot of a control does not prove it works.

Retained-feature expectations in the maps are acceptance requirements from the integration PRD. Missing endpoints, fields, controls, or preconditions are failures or explicit unverified limitations. Do not remove assertions to make upstream baseline pass.

## Cleanup

```sh
.agents/skills/verify/scripts/verify.py cleanup "$MANIFEST"
```

Cleanup sends SIGTERM only when PID start identity and executable match the manifest. It checks that the owned HTTP socket closes and writes `cleanup.json`. It preserves the config, logs, media, binary, and responses. Run cleanup after each failed attempt and after browser work. Confirm the proof files survive. Never kill by process name or shut down another agent's preview tab.

## Helpers

The executable [scripts/verify.py](scripts/verify.py) uses the Python standard library. Its commands are `launch`, `doctor`, `smoke`, and `cleanup`. It has no background polling watcher. Keep the maps aligned with source routes and selectors as TinyIce changes. The `pstack:maintain-verification-skill` workflow can audit them when requested.
