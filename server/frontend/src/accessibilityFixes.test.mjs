import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const volumeKnobPath = fileURLToPath(new URL('./components/VolumeKnob.tsx', import.meta.url))
const developersPath = fileURLToPath(new URL('./pages/Developers.tsx', import.meta.url))
const loginPath = fileURLToPath(new URL('./pages/Login.tsx', import.meta.url))
const setupPath = fileURLToPath(new URL('./pages/Setup.tsx', import.meta.url))
const explorePath = fileURLToPath(new URL('./pages/Explore.tsx', import.meta.url))
const usersPath = fileURLToPath(new URL('./pages/admin/Users.tsx', import.meta.url))
const streamsPath = fileURLToPath(new URL('./pages/admin/Streams.tsx', import.meta.url))

test('VolumeKnob supports keyboard and pointer slider interactions', async () => {
  const source = await readFile(volumeKnobPath, 'utf8')

  assert.match(source, /onKeyDown=/, 'Volume slider should support keyboard adjustments')
  assert.match(source, /onPointerDown=/, 'Volume slider should support pointer interactions for mouse and touch')
  assert.match(source, /aria-valuetext=/, 'Volume slider should expose a readable value for assistive tech')
})

test('Developers external links use noopener protection', async () => {
  const source = await readFile(developersPath, 'utf8')

  assert.match(source, /rel="noopener noreferrer"/, 'Developers external links should protect opener state when using target="_blank"')
})

test('Auth and search inputs expose explicit labels', async () => {
  const [loginSource, setupSource, exploreSource] = await Promise.all([
    readFile(loginPath, 'utf8'),
    readFile(setupPath, 'utf8'),
    readFile(explorePath, 'utf8'),
  ])

  assert.match(loginSource, /htmlFor="login-username"|aria-label="Username"/, 'Login username input should have an explicit label')
  assert.match(loginSource, /htmlFor="login-password"|aria-label="Password"/, 'Login password input should have an explicit label')
  assert.match(setupSource, /htmlFor="setup-token"|aria-label="Setup token"/, 'Setup token input should have an explicit label')
  assert.match(exploreSource, /htmlFor="stream-search"|aria-label="Search streams"/, 'Explore search input should have an explicit label')
})

test('Admin icon-only buttons expose accessible names', async () => {
  const [usersSource, streamsSource] = await Promise.all([
    readFile(usersPath, 'utf8'),
    readFile(streamsPath, 'utf8'),
  ])

  assert.match(usersSource, /aria-label="Edit user"|aria-label=\{`Edit /, 'Users actions should label edit buttons')
  assert.match(usersSource, /aria-label="Remove user"|aria-label=\{`Remove /, 'Users actions should label remove buttons')
  assert.match(streamsSource, /aria-label="Kick source"|aria-label=\{`Kick source/, 'Streams actions should label kick-source buttons')
  assert.match(streamsSource, /aria-label="Kick listeners"|aria-label=\{`Kick listeners/, 'Streams actions should label kick-listeners buttons')
  assert.match(streamsSource, /aria-label="Remove mount"|aria-label=\{`Remove mount/, 'Streams actions should label remove buttons')
})
