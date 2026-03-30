import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const dashboardPath = fileURLToPath(new URL('./admin/Dashboard.tsx', import.meta.url))
const studioPath = fileURLToPath(new URL('./admin/Studio.tsx', import.meta.url))
const autoDJPath = fileURLToPath(new URL('./admin/AutoDJ.tsx', import.meta.url))
const playerPath = fileURLToPath(new URL('./Player.tsx', import.meta.url))

test('Dashboard uses a page-local store factory', async () => {
  const source = await readFile(dashboardPath, 'utf8')
  assert.match(source, /createDashboardStore/, 'Dashboard should move signal ownership into a page-local store factory')
})

test('Studio uses a page-local store factory', async () => {
  const source = await readFile(studioPath, 'utf8')
  assert.match(source, /createStudioStore/, 'Studio should move signal ownership into a page-local store factory')
})

test('AutoDJ uses a page-local store factory', async () => {
  const source = await readFile(autoDJPath, 'utf8')
  assert.match(source, /createAutoDJStore/, 'AutoDJ should move signal ownership into a page-local store factory')
})

test('Player uses a page-local store factory', async () => {
  const source = await readFile(playerPath, 'utf8')
  assert.match(source, /createPlayerStore/, 'Player should move signal ownership into a page-local store factory')
})
