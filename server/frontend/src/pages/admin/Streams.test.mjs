import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const streamsPath = fileURLToPath(new URL('./Streams.tsx', import.meta.url))
const typesPath = fileURLToPath(new URL('../../types.ts', import.meta.url))

test('Streams page renders explicit diagnostic fields', async () => {
  const source = await readFile(streamsPath, 'utf8')

  assert.match(source, /status_reason/, 'Streams page should render the latest status reason')
  assert.match(source, /history/, 'Streams page should render recent diagnostic history')
  assert.match(source, /Recovering|Stopped|Dead|Degraded|Running|Error/, 'Streams page should map status badges')
})

test('shared frontend types include diagnostics payload fields', async () => {
  const source = await readFile(typesPath, 'utf8')

  assert.match(source, /status:\s*string/, 'Stream types should include diagnostic status')
  assert.match(source, /status_reason:\s*string/, 'Stream types should include status reason')
  assert.match(source, /history:\s*DiagnosticHistoryEntry\[\]/, 'Stream types should include diagnostic history')
})
