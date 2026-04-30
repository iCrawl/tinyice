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
  assert.match(source, /showHistoryModal/, 'Streams page should track history modal state')
  assert.match(source, /History for/, 'Streams page should expose a diagnostic history modal')
  assert.match(source, /details\?\.path/, 'Streams page should render song_command path details in the history modal')
  assert.match(source, /source_kind/, 'Streams page should read runtime source kind metadata')
  assert.match(source, /source_label/, 'Streams page should render a human-readable source label')
  assert.match(source, /Recovering|Stopped|Dead|Degraded|Running|Error/, 'Streams page should map status badges')
  assert.match(source, /self-start/, 'Streams page should keep status pills sized to their content')
  assert.match(source, /max_listeners/, 'Streams page should render mount-level listener caps')
  assert.match(source, /burst_size/, 'Streams page should render mount burst size settings')
  assert.match(source, /effective_burst_size/, 'Streams page should render effective mount burst settings')
  assert.match(source, /default/, 'Streams page should label defaulted burst settings')
  assert.match(source, /Edit Mount Settings/, 'Streams page should expose a modal for mount settings')
  assert.match(source, /openEditModal/, 'Streams page should open mount settings from a dedicated edit action')
  assert.match(source, /saveEditModal/, 'Streams page should save mount settings from the modal')
  assert.doesNotMatch(source, /slice\(-3\)\.reverse\(\)\.map\(\(entry\) => entry\.reason\)\.join\(' • '\)/, 'Streams page should not flatten multiple history reasons inline')
  assert.doesNotMatch(source, /title:"Save stream settings"/, 'Streams page should not keep inline save actions for mount settings')
})

test('shared frontend types include diagnostics payload fields', async () => {
  const source = await readFile(typesPath, 'utf8')

  assert.match(source, /status:\s*string/, 'Stream types should include diagnostic status')
  assert.match(source, /status_reason:\s*string/, 'Stream types should include status reason')
  assert.match(source, /history:\s*DiagnosticHistoryEntry\[\]/, 'Stream types should include diagnostic history')
  assert.match(source, /max_listeners:\s*number/, 'Stream types should include mount listener caps')
  assert.match(source, /burst_size:\s*number/, 'Stream types should include mount burst settings')
  assert.match(source, /effective_burst_size:\s*number/, 'Stream types should include effective mount burst settings')
})
