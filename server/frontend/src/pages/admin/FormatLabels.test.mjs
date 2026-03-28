import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const autoDJPath = fileURLToPath(new URL('./AutoDJ.tsx', import.meta.url))
const transcodersPath = fileURLToPath(new URL('./Transcoders.tsx', import.meta.url))

test('admin SPA format selectors use Ogg/Opus and do not expose standalone OGG', async () => {
  const [autoDJSource, transcodersSource] = await Promise.all([
    readFile(autoDJPath, 'utf8'),
    readFile(transcodersPath, 'utf8'),
  ])

  assert.match(
    autoDJSource,
    /<option value="opus">Ogg\/Opus<\/option>/,
    'AutoDJ should label the opus format as Ogg/Opus'
  )
  assert.doesNotMatch(
    autoDJSource,
    /<option value="ogg">OGG<\/option>/,
    'AutoDJ should not expose ogg as a separate selectable format'
  )
  assert.match(
    transcodersSource,
    /<option value="opus">Ogg\/Opus<\/option>/,
    'Transcoders should label the opus format as Ogg/Opus'
  )
})
