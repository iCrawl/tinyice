# Admin Listeners Bulk Move Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Update `/admin/listeners` to show mount-based totals, filter by available mounts via a select, and move multiple selected listeners through a modal.

**Architecture:** Keep the backend API unchanged and compute listener totals in the Preact page from `/api/listeners`. Reuse injected admin mount data from `window.__TINYICE__` for both the filter select and move target select. Replace row-local move inputs with page-level selection state and a modal-driven bulk action.

**Tech Stack:** Go backend shell injection, Preact, `@preact/signals`, Node source-level tests

---

### Task 1: Lock The Expected Page Contract In Tests

**Files:**
- Modify: `server/frontend/src/pages/admin/Listeners.test.mjs`
- Test: `server/frontend/src/pages/admin/Listeners.test.mjs`

- [ ] **Step 1: Write the failing test**

```js
assert.match(source, /window\.__TINYICE__/, 'Listeners page should read injected admin data')
assert.match(source, /type="checkbox"/, 'Listeners page should support row selection')
assert.match(source, /Move Selected/, 'Listeners page should expose a bulk move action')
assert.match(source, /showMoveModal|moveModalOpen/, 'Listeners page should track bulk move modal state')
assert.match(source, /<select|select/, 'Listeners page should render select controls for filtering and moving')
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test server/frontend/src/pages/admin/Listeners.test.mjs`
Expected: FAIL because the current page uses a text filter and row-local move input instead of selection plus modal state.

- [ ] **Step 3: Write minimal implementation**

```tsx
// Extend the page store with:
// - selectedIds
// - showMoveModal
// - bulkTargetMount
// - availableMounts from injected admin data
// Replace the text filter with a select.
// Replace row move inputs with row selection checkboxes.
// Add a bulk move button and modal.
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test server/frontend/src/pages/admin/Listeners.test.mjs`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/frontend/src/pages/admin/Listeners.tsx server/frontend/src/pages/admin/Listeners.test.mjs
git commit -m "feat: add bulk listener move workflow"
```

### Task 2: Implement Mount Totals And Filtered Selection Behavior

**Files:**
- Modify: `server/frontend/src/pages/admin/Listeners.tsx`
- Test: `server/frontend/src/pages/admin/Listeners.test.mjs`

- [ ] **Step 1: Write the failing test**

```js
assert.match(source, /All \(\$?\{?/, 'Listeners page should surface an all-listeners total label')
assert.match(source, /selectedCount|rows\.length/, 'Listeners page should compute counts from filtered rows')
assert.match(source, /filterMount\.value/, 'Listeners page should drive filtered rows from mount selection')
```

- [ ] **Step 2: Run test to verify it fails**

Run: `node --test server/frontend/src/pages/admin/Listeners.test.mjs`
Expected: FAIL because totals are not currently surfaced in the filter UI.

- [ ] **Step 3: Write minimal implementation**

```tsx
// Add helpers for:
// - available mount list
// - count by mount
// - filtered rows
// - visible selection state
// Render `All (N)` and `${mount} (${count})` in the filter select.
// Show the active total beside the page title.
```

- [ ] **Step 4: Run test to verify it passes**

Run: `node --test server/frontend/src/pages/admin/Listeners.test.mjs`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/frontend/src/pages/admin/Listeners.tsx server/frontend/src/pages/admin/Listeners.test.mjs
git commit -m "feat: add listener mount totals"
```

### Task 3: Verify The Final Frontend Slice

**Files:**
- Modify: `server/frontend/src/pages/admin/Listeners.tsx`
- Test: `server/frontend/src/pages/admin/Listeners.test.mjs`

- [ ] **Step 1: Run the focused test suite**

Run: `node --test server/frontend/src/pages/admin/Listeners.test.mjs`
Expected: PASS

- [ ] **Step 2: Run the broader lightweight frontend regression checks**

Run: `node --test server/frontend/src/pages/pageStateFactories.test.mjs server/frontend/src/accessibilityFixes.test.mjs`
Expected: PASS

- [ ] **Step 3: Inspect the final diff**

Run: `git diff -- server/frontend/src/pages/admin/Listeners.tsx server/frontend/src/pages/admin/Listeners.test.mjs`
Expected: Diff only contains listeners page and test updates needed for the new workflow.

- [ ] **Step 4: Commit**

```bash
git add server/frontend/src/pages/admin/Listeners.tsx server/frontend/src/pages/admin/Listeners.test.mjs
git commit -m "test: cover listeners bulk move workflow"
```
