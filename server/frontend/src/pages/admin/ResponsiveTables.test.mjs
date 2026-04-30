import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const globalsPath = fileURLToPath(new URL('../../globals.css', import.meta.url))
const adminLayoutPath = fileURLToPath(new URL('./AdminLayout.tsx', import.meta.url))

const TABLE_PAGES = [
  {
    label: 'Dashboard',
    path: fileURLToPath(new URL('./Dashboard.tsx', import.meta.url)),
    minWidth: 'min-w-[700px]',
  },
  {
    label: 'Streams',
    path: fileURLToPath(new URL('./Streams.tsx', import.meta.url)),
    minWidth: 'min-w-[980px]',
  },
  {
    label: 'Listeners',
    path: fileURLToPath(new URL('./Listeners.tsx', import.meta.url)),
    minWidth: 'min-w-[1080px]',
  },
  {
    label: 'Users',
    path: fileURLToPath(new URL('./Users.tsx', import.meta.url)),
    minWidth: 'min-w-[640px]',
  },
  {
    label: 'Security',
    path: fileURLToPath(new URL('./Security.tsx', import.meta.url)),
    minWidth: 'min-w-[640px]',
  },
  {
    label: 'API Tokens',
    path: fileURLToPath(new URL('./APITokens.tsx', import.meta.url)),
    minWidth: 'min-w-[980px]',
  },
]

test('shared admin table shell enables horizontal scrolling', async () => {
  const source = await readFile(globalsPath, 'utf8')

  assert.match(source, /\.admin-table-shell\s*\{/, 'Globals should define a shared admin table shell')
  assert.match(source, /\.admin-table-scroll\s*\{[\s\S]*overflow-x:\s*auto;/, 'Shared table shell should allow horizontal scrolling')
  assert.match(source, /-webkit-overflow-scrolling:\s*touch;/, 'Shared table shell should preserve touch scrolling momentum')
})

test('admin layout allows inner scroll containers to shrink on mobile', async () => {
  const source = await readFile(adminLayoutPath, 'utf8')

  assert.match(source, /<main class="[^"]*min-w-0/, 'Admin layout main area should allow responsive tables to shrink within the flex shell')
})

for (const tablePage of TABLE_PAGES) {
  test(`${tablePage.label} page uses the responsive admin table shell`, async () => {
    const source = await readFile(tablePage.path, 'utf8')

    assert.match(source, /admin-table-shell/, `${tablePage.label} should use the shared table shell`)
    assert.match(source, /admin-table-scroll/, `${tablePage.label} should use the shared horizontal scroll wrapper`)
    assert.match(source, new RegExp(tablePage.minWidth.replace('[', '\\[').replace(']', '\\]')), `${tablePage.label} should set a mobile-safe minimum table width`)
  })
}
