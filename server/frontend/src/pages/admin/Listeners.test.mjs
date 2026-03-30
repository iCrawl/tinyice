import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const listenersPath = fileURLToPath(new URL('./Listeners.tsx', import.meta.url))
const layoutPath = fileURLToPath(new URL('./AdminLayout.tsx', import.meta.url))
const sidebarPath = fileURLToPath(new URL('../../components/Sidebar.tsx', import.meta.url))
const typesPath = fileURLToPath(new URL('../../types.ts', import.meta.url))

test('Listeners page wires list, move, and disconnect actions', async () => {
  const source = await readFile(listenersPath, 'utf8')

  assert.match(source, /\/api\/listeners/, 'Listeners page should load active listeners')
  assert.match(source, /\/api\/listeners\/disconnect/, 'Listeners page should disconnect listeners')
  assert.match(source, /\/api\/listeners\/move/, 'Listeners page should move listeners')
  assert.match(source, /target_mount/, 'Listeners page should submit target mounts')
  assert.match(source, /current_mount/, 'Listeners page should render current mount')
})

test('Admin navigation exposes the listeners page', async () => {
  const [layoutSource, sidebarSource] = await Promise.all([
    readFile(layoutPath, 'utf8'),
    readFile(sidebarPath, 'utf8'),
  ])

  assert.match(layoutSource, /\/admin\/listeners/, 'Admin router should register the listeners page')
  assert.match(sidebarSource, /\/admin\/listeners/, 'Sidebar should link to the listeners page')
})

test('shared frontend types include listener snapshots', async () => {
  const source = await readFile(typesPath, 'utf8')

  assert.match(source, /interface ListenerInfo/, 'Shared types should include a listener snapshot shape')
  assert.match(source, /current_mount:\s*string/, 'Listener type should include the active mount')
  assert.match(source, /requested_mount:\s*string/, 'Listener type should include the requested mount')
  assert.match(source, /protocol:\s*'http' \| 'webrtc'|protocol:\s*string/, 'Listener type should include the protocol')
})
