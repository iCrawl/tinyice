# Ogg/Opus Labeling Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make all user-facing format labels explicit as `Ogg/Opus` while keeping stored and API format values as `opus`.

**Architecture:** Treat this as a user-facing terminology cleanup, not a media-pipeline change. Update the SPA, legacy template, and OpenAPI text in one pass, and pin the behavior with lightweight source-level regression tests so `ogg` does not reappear as a separate selectable format.

**Tech Stack:** Preact/TypeScript source tests via `node --test`, Go regression tests, existing `server` package, OpenAPI YAML, legacy HTML templates

---

## File Structure

- Create: `server/frontend/src/pages/admin/FormatLabels.test.mjs`
  - Source-level regression tests for SPA format selectors in `AutoDJ.tsx` and `Transcoders.tsx`.
- Create: `server/labeling_regressions_test.go`
  - Regression tests for the legacy admin template and OpenAPI schema text.
- Modify: `server/frontend/src/pages/admin/AutoDJ.tsx`
  - Replace the `Opus` label with `Ogg/Opus` and remove the standalone `OGG` option.
- Modify: `server/frontend/src/pages/admin/Transcoders.tsx`
  - Replace the `Opus` label with `Ogg/Opus`.
- Modify: `server/templates/admin.html`
  - Replace legacy `Opus` labels with `Ogg/Opus` in transcoder and AutoDJ selectors, and update nearby placeholder text.
- Modify: `server/openapi.yaml`
  - Remove `ogg` from the format enum and update descriptions so they describe the `opus` value as Ogg/Opus output.

### Task 1: Lock the SPA labels to `Ogg/Opus`

**Files:**
- Create: `server/frontend/src/pages/admin/FormatLabels.test.mjs`
- Modify: `server/frontend/src/pages/admin/AutoDJ.tsx`
- Modify: `server/frontend/src/pages/admin/Transcoders.tsx`

- [ ] **Step 1: Write the failing SPA label regression test**

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const autoDJPath = fileURLToPath(new URL('./AutoDJ.tsx', import.meta.url))
const transcodersPath = fileURLToPath(new URL('./Transcoders.tsx', import.meta.url))

test('admin SPA format selectors use Ogg/Opus and do not expose standalone OGG', async () => {
  const [autoDJSource, transcodersSource] = await Promise.all([
    readFile(autoDJPath, 'utf8'),
    readFile(transcodersPath, 'utf8'),
  ])

  assert.match(
    autoDJSource,
    /<option value="opus">Ogg\/Opus<\/option>/,
    'AutoDJ should label the opus format as Ogg/Opus'
  )
  assert.doesNotMatch(
    autoDJSource,
    /<option value="ogg">OGG<\/option>/,
    'AutoDJ should not expose ogg as a separate selectable format'
  )
  assert.match(
    transcodersSource,
    /<option value="opus">Ogg\/Opus<\/option>/,
    'Transcoders should label the opus format as Ogg/Opus'
  )
})
```

- [ ] **Step 2: Run the SPA label regression test to verify it fails first**

Run: `cd server/frontend && node --test src/pages/admin/FormatLabels.test.mjs`

Expected: FAIL because `AutoDJ.tsx` still contains `<option value="opus">Opus</option>` and still exposes `<option value="ogg">OGG</option>`.

- [ ] **Step 3: Update the SPA selectors to match the approved terminology**

```tsx
// server/frontend/src/pages/admin/AutoDJ.tsx
<option value="mp3">MP3</option>
<option value="opus">Ogg/Opus</option>
```

```tsx
// server/frontend/src/pages/admin/Transcoders.tsx
<option value="mp3">MP3</option>
<option value="opus">Ogg/Opus</option>
```

Remove this block entirely from `server/frontend/src/pages/admin/AutoDJ.tsx`:

```tsx
<option value="ogg">OGG</option>
```

- [ ] **Step 4: Re-run the SPA tests and confirm they pass**

Run: `cd server/frontend && node --test src/pages/admin/AutoDJ.test.mjs src/pages/admin/FormatLabels.test.mjs`

Expected: PASS

- [ ] **Step 5: Commit the SPA labeling cleanup**

```bash
git add server/frontend/src/pages/admin/AutoDJ.tsx \
  server/frontend/src/pages/admin/Transcoders.tsx \
  server/frontend/src/pages/admin/FormatLabels.test.mjs
git commit -m "ui: label opus output as ogg/opus"
```

### Task 2: Align the legacy admin template and OpenAPI docs with the `opus` config value

**Files:**
- Create: `server/labeling_regressions_test.go`
- Modify: `server/templates/admin.html`
- Modify: `server/openapi.yaml`

- [ ] **Step 1: Write the failing server-side regression test**

```go
package server

import (
	"bytes"
	"os"
	"testing"
)

func TestOggOpusLabelingRemainsConsistent(t *testing.T) {
	adminHTML, err := os.ReadFile("templates/admin.html")
	if err != nil {
		t.Fatalf("read admin template: %v", err)
	}

	for _, want := range [][]byte{
		[]byte(`<option value="opus">Ogg/Opus</option>`),
		[]byte(`placeholder="MP3 to Ogg/Opus"`),
	} {
		if !bytes.Contains(adminHTML, want) {
			t.Fatalf("admin template missing %q", want)
		}
	}
	if bytes.Contains(adminHTML, []byte(`<option value="ogg">OGG</option>`)) {
		t.Fatal("admin template should not expose ogg as a separate selectable format")
	}

	openAPI, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}
	if !bytes.Contains(openAPI, []byte(`enum: [mp3, opus]`)) {
		t.Fatal("OpenAPI should advertise mp3 and opus as the accepted format values")
	}
	if bytes.Contains(openAPI, []byte(`enum: [mp3, opus, ogg]`)) {
		t.Fatal("OpenAPI should not advertise ogg as a separate format value")
	}
	if !bytes.Contains(openAPI, []byte(`configured for that mount (MP3 or Ogg/Opus).`)) {
		t.Fatal("OpenAPI stream description should describe the output as MP3 or Ogg/Opus")
	}
}
```

- [ ] **Step 2: Run the regression test to confirm the current docs/template still fail**

Run: `go test ./server -run TestOggOpusLabelingRemainsConsistent -count=1`

Expected: FAIL because `server/templates/admin.html` still says `Opus`, and `server/openapi.yaml` still contains `enum: [mp3, opus, ogg]`.

- [ ] **Step 3: Update the legacy admin template and OpenAPI wording**

```html
<!-- server/templates/admin.html -->
<input type="text" name="name" placeholder="MP3 to Ogg/Opus" required>

<select name="format">
    <option value="mp3">MP3</option>
    <option value="opus">Ogg/Opus</option>
</select>

<select name="format" id="edit-format">
    <option value="mp3">MP3</option>
    <option value="opus">Ogg/Opus</option>
</select>
```

```yaml
# server/openapi.yaml
description: |
  Connect as a listener to receive the audio stream via HTTP.
  The server responds with a continuous audio stream in the format
  configured for that mount (MP3 or Ogg/Opus).
```

```yaml
# server/openapi.yaml
format:
  type: string
  enum: [mp3, opus]
  default: mp3
  description: Output format. Use `opus` for Ogg/Opus streams.
```

- [ ] **Step 4: Re-run the focused regression test**

Run: `go test ./server -run TestOggOpusLabelingRemainsConsistent -count=1`

Expected: PASS

- [ ] **Step 5: Run the combined verification pass**

Run: `cd server/frontend && node --test src/pages/admin/AutoDJ.test.mjs src/pages/admin/FormatLabels.test.mjs && cd /home/crawl/git/tinyice && go test ./server -run 'Test(Review|OggOpusLabeling)' -count=1`

Expected: PASS

- [ ] **Step 6: Commit the server/docs cleanup**

```bash
git add server/templates/admin.html \
  server/openapi.yaml \
  server/labeling_regressions_test.go
git commit -m "docs: clarify ogg/opus format labeling"
```
