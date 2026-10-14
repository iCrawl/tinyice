import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('./metadata.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText
const { applyMetadata, applyStreamTitle } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const initial = { title: 'Initial track', artist: 'Station', revision: null, versioned: false }
const event = { mount: '/live', title: 'New track', artist: 'Station', started_at: '2026-10-14T12:00:00Z', playback_id: 'play-1', revision: 12 }

test('metadata replay replaces the initial page title', () => {
  assert.deepEqual(applyMetadata(initial, event), { title: 'New track', artist: 'Station', revision: 12, versioned: true })
})

test('explicit clears survive subsequent unversioned stream stats', () => {
  const cleared = applyMetadata(applyMetadata(initial, event), { ...event, title: '', artist: '', playback_id: '', revision: 13 })
  assert.deepEqual(applyStreamTitle(cleared, { title: 'Old track', artist: 'Station' }), { title: '', artist: '', revision: 13, versioned: true })
})

test('older metadata cannot replace a newer track or clear', () => {
  const cleared = applyMetadata(initial, { ...event, title: '', artist: '', revision: 13 })
  assert.deepEqual(applyMetadata(cleared, event), { title: '', artist: '', revision: 13, versioned: true })
})

test('a replay after a server restart can accept a new revision sequence', () => {
  const disconnected = { ...applyMetadata(initial, event), revision: null }
  assert.equal(applyStreamTitle(disconnected, { title: 'Stale stats', artist: 'Station' }).title, 'New track')
  assert.deepEqual(applyMetadata(disconnected, { ...event, title: 'Restarted track', revision: 1 }), { title: 'Restarted track', artist: 'Station', revision: 1, versioned: true })
})

test('unversioned servers continue to update titles until metadata is received', () => {
  assert.deepEqual(applyStreamTitle(initial, { title: '', artist: '' }), { title: '', artist: '', revision: null, versioned: false })
})

test('repeated titles advance playback revisions', () => {
  assert.equal(applyMetadata(applyMetadata(initial, event), { ...event, playback_id: 'play-2', revision: 14 }).revision, 14)
})
