import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const adminEntryPath = fileURLToPath(new URL('../../entries/admin.tsx', import.meta.url))
const settingsPath = fileURLToPath(new URL('./Settings.tsx', import.meta.url))

test('admin entry applies the injected branding accent color on boot', async () => {
  const source = await readFile(adminEntryPath, 'utf8')

  assert.match(
    source,
    /applyBrandingTheme\(\s*data\.branding\?\.accentColor\s*\)/,
    'Admin boot must apply the injected branding accent color so the admin UI matches saved branding on first render'
  )
})

test('branding saves update the active admin accent theme', async () => {
  const source = await readFile(settingsPath, 'utf8')

  assert.match(
    source,
    /applyBrandingTheme\(\s*branding\.value\.accent_color\s*\)/,
    'Saving branding must update the current admin theme so the admin UI changes immediately'
  )
})
