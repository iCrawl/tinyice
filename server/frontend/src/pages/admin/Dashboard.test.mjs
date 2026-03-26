import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const dashboardPath = fileURLToPath(new URL('./Dashboard.tsx', import.meta.url))

test('Dashboard traffic chart does not derive CSS height from raw listener count', async () => {
  const source = await readFile(dashboardPath, 'utf8')

  assert.doesNotMatch(
    source,
    /stats\.value\.listeners\s*:\s*0\)\s*\*\s*10\)\s*}%/,
    'Listener Traffic bars must be normalized to the chart range instead of multiplying listeners into CSS percentages'
  )
})
