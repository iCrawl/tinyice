import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const studioPath = fileURLToPath(new URL('./admin/Studio.tsx', import.meta.url))
const autoDJPath = fileURLToPath(new URL('./admin/AutoDJ.tsx', import.meta.url))
const loginPath = fileURLToPath(new URL('./Login.tsx', import.meta.url))
const setupPath = fileURLToPath(new URL('./Setup.tsx', import.meta.url))
const explorePath = fileURLToPath(new URL('./Explore.tsx', import.meta.url))
const embedPath = fileURLToPath(new URL('./Embed.tsx', import.meta.url))
const usersPath = fileURLToPath(new URL('./admin/Users.tsx', import.meta.url))
const streamsPath = fileURLToPath(new URL('./admin/Streams.tsx', import.meta.url))
const apiTokensPath = fileURLToPath(new URL('./admin/APITokens.tsx', import.meta.url))
const settingsPath = fileURLToPath(new URL('./admin/Settings.tsx', import.meta.url))
const listenersPath = fileURLToPath(new URL('./admin/Listeners.tsx', import.meta.url))
const transcodersPath = fileURLToPath(new URL('./admin/Transcoders.tsx', import.meta.url))
const pendingUsersPath = fileURLToPath(new URL('./admin/PendingUsers.tsx', import.meta.url))
const relaysPath = fileURLToPath(new URL('./admin/Relays.tsx', import.meta.url))
const securityPath = fileURLToPath(new URL('./admin/Security.tsx', import.meta.url))
const goLivePath = fileURLToPath(new URL('./admin/GoLive.tsx', import.meta.url))
const passkeyButtonPath = fileURLToPath(new URL('../components/PasskeyButton.tsx', import.meta.url))

test('Studio uses a page-local store factory', async () => {
  const source = await readFile(studioPath, 'utf8')
  assert.match(source, /createStudioStore/, 'Studio should move signal ownership into a page-local store factory')
})

test('AutoDJ uses a page-local store factory', async () => {
  const source = await readFile(autoDJPath, 'utf8')
  assert.match(source, /createAutoDJStore/, 'AutoDJ should move signal ownership into a page-local store factory')
})

test('Login uses a page-local store factory', async () => {
  const source = await readFile(loginPath, 'utf8')
  assert.match(source, /createLoginStore/, 'Login should keep auth state in a page-local store factory')
})

test('Setup uses a page-local store factory', async () => {
  const source = await readFile(setupPath, 'utf8')
  assert.match(source, /createSetupStore/, 'Setup should keep wizard state in a page-local store factory')
})

test('Explore uses a page-local store factory', async () => {
  const source = await readFile(explorePath, 'utf8')
  assert.match(source, /createExploreStore/, 'Explore should keep listing state in a page-local store factory')
})

test('Embed uses a page-local store factory', async () => {
  const source = await readFile(embedPath, 'utf8')
  assert.match(source, /createEmbedStore/, 'Embed should keep player state in a page-local store factory')
})

test('PasskeyButton uses a component-local store factory', async () => {
  const source = await readFile(passkeyButtonPath, 'utf8')
  assert.match(source, /createPasskeyButtonStore/, 'PasskeyButton should avoid module-scope loading and error signals')
})

test('Users uses a page-local store factory', async () => {
  const source = await readFile(usersPath, 'utf8')
  assert.match(source, /createUsersStore/, 'Users should avoid module-scope admin state')
})

test('Streams uses a page-local store factory', async () => {
  const source = await readFile(streamsPath, 'utf8')
  assert.match(source, /createStreamsStore/, 'Streams should avoid module-scope admin state')
})

test('APITokens uses a page-local store factory', async () => {
  const source = await readFile(apiTokensPath, 'utf8')
  assert.match(source, /createAPITokensStore/, 'APITokens should avoid module-scope admin state')
})

test('Settings uses a page-local store factory', async () => {
  const source = await readFile(settingsPath, 'utf8')
  assert.match(source, /createSettingsStore/, 'Settings should avoid module-scope admin state')
})

test('Listeners uses a page-local store factory', async () => {
  const source = await readFile(listenersPath, 'utf8')
  assert.match(source, /createListenersStore/, 'Listeners should avoid module-scope admin state')
})

test('Transcoders uses a page-local store factory', async () => {
  const source = await readFile(transcodersPath, 'utf8')
  assert.match(source, /createTranscodersStore/, 'Transcoders should avoid module-scope admin state')
})

test('PendingUsers uses a page-local store factory', async () => {
  const source = await readFile(pendingUsersPath, 'utf8')
  assert.match(source, /createPendingUsersStore/, 'PendingUsers should avoid module-scope admin state')
})

test('Relays uses a page-local store factory', async () => {
  const source = await readFile(relaysPath, 'utf8')
  assert.match(source, /createRelaysStore/, 'Relays should avoid module-scope admin state')
})

test('Security uses a page-local store factory', async () => {
  const source = await readFile(securityPath, 'utf8')
  assert.match(source, /createSecurityStore/, 'Security should avoid module-scope admin state')
})

test('GoLive uses a page-local store factory', async () => {
  const source = await readFile(goLivePath, 'utf8')
  assert.match(source, /createGoLiveStore/, 'GoLive should avoid module-scope admin state')
})
