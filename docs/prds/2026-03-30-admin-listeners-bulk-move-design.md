# Admin Listeners Bulk Move Design

## Goal

Improve `/admin/listeners` so admins can see mount-scoped listener totals, filter by available mounts via a select control, and move multiple selected listeners through a modal instead of a per-row text input.

## Current State

- The page loads active listeners from `/api/listeners`.
- Mount filtering is a free-text input, so it does not reflect the actual allowed mount list.
- The page does not surface total counts per mount or for the current filter.
- Moving a listener is row-local and depends on a text input embedded in each row.

## Design

### Mount Filter And Totals

- Replace the free-text mount filter with a `select` populated from `window.__TINYICE__.mounts`.
- The first option is `All (N)` where `N` is the total number of visible listeners.
- Each mount option is labeled with its live count, for example `/live (12)`.
- The active selection drives the table rows.
- The page header also shows the active total so the count remains visible without opening the select.

### Bulk Selection

- Add a selection column with row checkboxes.
- Add a header checkbox that selects all listeners currently visible under the active filter.
- Selection is tracked by listener id and is preserved only while the current list is loaded.
- After a reload, selection is reconciled so missing listeners are dropped.

### Move Selected Modal

- Remove the per-row `Move To` input and `MOVE` button.
- Add a `Move Selected` button above the table.
- Clicking `Move Selected` opens a modal that:
  - shows how many listeners are selected
  - offers a target mount `select` built from available mounts
  - disables submission until at least one listener is selected and a target mount is chosen
- Submitting the modal issues one `/api/listeners/move` request per selected listener, then reloads the page data and clears the selection.

### Error Handling

- Keep the existing client API wrapper behavior for non-2xx responses.
- For now, bulk move stops on the first request failure. This keeps the implementation small and avoids inventing a partial-success UI that the rest of the admin panel does not use.
- WebRTC listeners remain selectable. If the server rejects moving them with the existing conflict response, the modal submission fails and the page reload behavior stays unchanged.

## Data Flow

- Reuse the existing `/api/listeners` payload to compute per-mount counts on the client.
- Reuse injected admin `mounts` data for filter options and move targets.
- No backend API changes are required.

## Testing

- Extend the existing listeners page source-level test to assert:
  - the page reads injected admin mounts
  - the page renders a select-based filter
  - the page tracks selected listeners
  - the page includes a bulk move modal flow
  - the page still calls `/api/listeners`, `/api/listeners/disconnect`, and `/api/listeners/move`

## Scope

- In scope: listeners page UI/state changes and focused frontend tests.
- Out of scope: backend API changes, aggregated move result reporting, and transport-specific handling for WebRTC beyond current API behavior.
