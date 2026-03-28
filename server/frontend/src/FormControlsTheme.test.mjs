import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const globalsPath = fileURLToPath(new URL('./globals.css', import.meta.url))

test('global form controls theme select menus for dark surfaces', async () => {
  const source = await readFile(globalsPath, 'utf8')

  assert.match(
    source,
    /select,\s*option,\s*optgroup\s*\{/,
    'Shared globals must style native select menus and their options instead of leaving dropdown rows to browser defaults'
  )

  assert.match(
    source,
    /background-color:\s*var\(--color-surface-overlay\)/,
    'Select menus should use the shared dark surface background so option lists stay readable'
  )

  assert.match(
    source,
    /color:\s*var\(--color-text-primary\)/,
    'Select menus should use the shared primary text color so options remain legible on dark backgrounds'
  )
})
