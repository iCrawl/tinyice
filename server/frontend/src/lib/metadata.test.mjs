import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('./metadata.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText
const { applyMetadata, applyStreamTitle, createMetadataCallbacks } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
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

function publicFeed() {
  let displayed = initial
  const callbacks = createMetadataCallbacks({
    mount: '/live',
    initial,
    update(value) { displayed = value },
  })
  return { callbacks, displayed: () => displayed }
}

const stream = { ...event, format: 'MP3', bitrate: 128, listeners: 2, health: 100 }

test('public player callbacks reconcile an unlisted track through its named stream tick', () => {
  const feed = publicFeed()
  feed.callbacks.onOpen()
  feed.callbacks.metadata(event)
  feed.callbacks.stream({ ...stream, title: 'Unlisted track', playback_id: 'play-2', revision: 13 })
  assert.deepEqual(feed.displayed(), { title: 'Unlisted track', artist: 'Station', revision: 13, versioned: true })
})

test('a stream tick repairs the last dropped metadata update and clear', () => {
  const feed = publicFeed()
  feed.callbacks.metadata(event)
  feed.callbacks.stream({ ...stream, title: 'Final track', revision: 30 })
  assert.deepEqual(feed.displayed(), { title: 'Final track', artist: 'Station', revision: 30, versioned: true })
  feed.callbacks.stream({ ...stream, title: '', artist: '', started_at: '', playback_id: '', revision: 31 })
  feed.callbacks.metadata({ ...event, title: 'Delayed track', revision: 29 })
  feed.callbacks.stream({ ...stream, title: 'Stale snapshot', revision: 30 })
  feed.callbacks.stream({ mount: '/live', title: 'Stale legacy stats', artist: 'Station' })
  assert.deepEqual(feed.displayed(), { title: '', artist: '', revision: 31, versioned: true })
})

test('reconnection while unlisted accepts a restarted stream revision sequence', () => {
  const feed = publicFeed()
  feed.callbacks.metadata({ ...event, revision: 300 })
  feed.callbacks.onOpen()
  feed.callbacks.stream({ ...stream, title: 'Restarted unlisted track', revision: 1 })
  assert.deepEqual(feed.displayed(), { title: 'Restarted unlisted track', artist: 'Station', revision: 1, versioned: true })
})

test('reconnection keeps explicit clear precedence until an authoritative snapshot arrives', () => {
  const feed = publicFeed()
  feed.callbacks.metadata({ ...event, title: '', artist: '', revision: 13 })
  feed.callbacks.onOpen()
  feed.callbacks.stream({ mount: '/live', title: 'Old stats', artist: 'Station' })
  assert.deepEqual(feed.displayed(), { title: '', artist: '', revision: null, versioned: true })
  feed.callbacks.stream({ ...stream, title: 'Current track', revision: 1 })
  assert.deepEqual(feed.displayed(), { title: 'Current track', artist: 'Station', revision: 1, versioned: true })
})

test('public callbacks ignore metadata and stream snapshots for other mounts', () => {
  const feed = publicFeed()
  feed.callbacks.metadata(event)
  feed.callbacks.metadata({ ...event, mount: '/other', title: 'Other track', revision: 40 })
  feed.callbacks.stream({ ...stream, mount: '/other', title: '', artist: '', revision: 41 })
  assert.deepEqual(feed.displayed(), { title: 'New track', artist: 'Station', revision: 12, versioned: true })
})
