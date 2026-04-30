import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const studioPath = fileURLToPath(new URL('./Studio.tsx', import.meta.url))

test('Studio page subscribes to the authenticated admin SSE stream', async () => {
  const source = await readFile(studioPath, 'utf8')

  assert.match(
    source,
    /createSSE\(\s*['"]\/admin\/events['"]\s*\)/,
    'Studio should consume the authenticated admin SSE stream instead of the public /events feed'
  )
})
