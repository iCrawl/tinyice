# Ogg/Opus Labeling Cleanup — Design Spec

## Goal

Make the product explicit that TinyIce currently delivers Opus audio in an Ogg container, while avoiding any backend format migration or runtime behavior change.

## Problem Summary

The codebase implements a meaningful output choice between `mp3` and `opus`, where the `opus` path is served as `audio/ogg`.

That behavior is clear in the runtime code, but the user-facing surfaces are inconsistent:

- the legacy admin UI exposes `MP3` and `Opus`
- the new AutoDJ UI exposes `MP3`, `Opus`, and a separate `OGG` option
- the OpenAPI schema suggests `ogg` is a distinct accepted format

This is misleading because `ogg` is not implemented as a separate output mode. The real semantic choice is codec plus container: Ogg/Opus.

## Non-Goals

- No change to stored config values.
- No change to encoder selection logic.
- No support for Vorbis or any non-Opus Ogg output.
- No migration of existing `format: "opus"` values.
- No change to listener-side content types or stream transport.

## Chosen Approach

Keep the internal format value as `opus`, but change all user-facing labels to `Ogg/Opus`.

Remove the separate `OGG` option from the new AutoDJ UI and update documentation surfaces so they no longer imply that `ogg` is a separate selectable format.

## Alternatives Considered

### 1. Keep the current `Opus` label

This is shorter, but it hides the transport detail that prompted the confusion. It leaves users guessing whether `Opus` means raw Opus, Ogg/Opus, or something else.

### 2. Rename the internal value from `opus` to `ogg`

This would align the stored value with the transport container, but it would introduce unnecessary config churn and code changes because the runtime already keys off `opus` as the meaningful encoder choice.

## Scope

Update the following surfaces:

- new SPA AutoDJ format labels
- legacy admin template format labels
- OpenAPI format enum and description text
- any nearby user-facing text that currently says only `Opus` when it means `Ogg/Opus`

Do not change backend branching that currently checks for `mp3` vs `opus`.

## UX Rules

- Show `MP3` as-is.
- Show `Ogg/Opus` instead of `Opus`.
- Do not show a standalone `OGG` option.
- Keep existing saved `opus` values working without conversion.

## API Rules

The API should continue accepting `opus` as the format value.

The public schema and examples should describe this as Ogg/Opus output rather than implying that `ogg` is a separate selectable mode.

## Testing

Verify:

- the SPA AutoDJ form offers only `MP3` and `Ogg/Opus`
- the legacy admin UI offers only `MP3` and `Ogg/Opus`
- API docs no longer advertise `ogg` as a distinct format value
- existing `format: "opus"` configs still render correctly in the UI

## Risks

The main risk is partial cleanup, where one surface still says `Opus` or still exposes `OGG`. This is low-risk but user-visible, so the implementation should update all known format selectors and docs in one pass.
