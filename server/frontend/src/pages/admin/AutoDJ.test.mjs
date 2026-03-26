import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const autoDJPath = fileURLToPath(new URL('./AutoDJ.tsx', import.meta.url))

test('AutoDJ page does not subscribe to stream SSE events', async () => {
  const source = await readFile(autoDJPath, 'utf8')

  assert.doesNotMatch(
    source,
    /sse\.on\(\s*['"]stream['"]/,
    'AutoDJ should not refetch on stream events because /events emits them continuously'
  )
})
