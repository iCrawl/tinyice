import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const adminEntryPath = fileURLToPath(new URL('../../entries/admin.tsx', import.meta.url))
const loginEntryPath = fileURLToPath(new URL('../../entries/login.tsx', import.meta.url))
const setupEntryPath = fileURLToPath(new URL('../../entries/setup.tsx', import.meta.url))
const landingEntryPath = fileURLToPath(new URL('../../entries/landing.tsx', import.meta.url))
const exploreEntryPath = fileURLToPath(new URL('../../entries/explore.tsx', import.meta.url))
const playerEntryPath = fileURLToPath(new URL('../../entries/player.tsx', import.meta.url))
const embedEntryPath = fileURLToPath(new URL('../../entries/embed.tsx', import.meta.url))
const developersEntryPath = fileURLToPath(new URL('../../entries/developers.tsx', import.meta.url))
const settingsPath = fileURLToPath(new URL('./Settings.tsx', import.meta.url))
const landingPagePath = fileURLToPath(new URL('../Landing.tsx', import.meta.url))
const playerPagePath = fileURLToPath(new URL('../Player.tsx', import.meta.url))
const transportControlsPath = fileURLToPath(new URL('../../components/TransportControls.tsx', import.meta.url))
const autoDjPath = fileURLToPath(new URL('./AutoDJ.tsx', import.meta.url))
const codeBlockPath = fileURLToPath(new URL('../../components/CodeBlock.tsx', import.meta.url))

test('all shell entry points apply the injected branding accent color on boot', async () => {
  const entries = [
    ['admin', adminEntryPath],
    ['login', loginEntryPath],
    ['setup', setupEntryPath],
    ['landing', landingEntryPath],
    ['explore', exploreEntryPath],
    ['player', playerEntryPath],
    ['embed', embedEntryPath],
    ['developers', developersEntryPath],
  ]

  for (const [entryName, entryPath] of entries) {
    const source = await readFile(entryPath, 'utf8')

    assert.match(
      source,
      /applyBrandingTheme\(\s*data\.branding\?\.accentColor\s*\)/,
      `${entryName} boot must apply the injected branding accent color so the page matches saved branding on first render`
    )
  }
})

test('branding saves update the active admin accent theme', async () => {
  const source = await readFile(settingsPath, 'utf8')

  assert.match(
    source,
    /applyBrandingTheme\(\s*branding\.value\.accent_color\s*\)/,
    'Saving branding must update the current admin theme so the admin UI changes immediately'
  )
})

test('accent-driven pages do not hardcode the default orange styling', async () => {
  const landingSource = await readFile(landingPagePath, 'utf8')

  assert.doesNotMatch(
    landingSource,
    /['"]--color-accent['"]\s*:/,
    'Landing should use the shared branding theme variables instead of overriding only --color-accent locally'
  )

  const accentDrivenFiles = [
    ['landing page', landingPagePath],
    ['player page', playerPagePath],
    ['transport controls', transportControlsPath],
    ['admin autodj', autoDjPath],
    ['code block', codeBlockPath],
  ]

  for (const [label, path] of accentDrivenFiles) {
    const source = await readFile(path, 'utf8')

    assert.doesNotMatch(
      source,
      /255,\s*102,\s*0|#ff6600/i,
      `${label} should derive accent visuals from theme variables instead of hardcoding the default orange`
    )
  }
})
