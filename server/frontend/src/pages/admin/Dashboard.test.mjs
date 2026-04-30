import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const dashboardPath = fileURLToPath(new URL('./Dashboard.tsx', import.meta.url))

test('Dashboard labels inbound and outbound cards as totals with byte units', async () => {
  const source = await readFile(dashboardPath, 'utf8')

  assert.match(
    source,
    /label="Inbound Total"/,
    'Dashboard should label inbound traffic as a cumulative total so it matches the backend counters'
  )

  assert.match(
    source,
    /label="Outbound Total"/,
    'Dashboard should label outbound traffic as a cumulative total so it matches the backend counters'
  )

  assert.match(
    source,
    /function formatBytes\(/,
    'Dashboard should format cumulative traffic with byte units instead of bandwidth-rate units'
  )

  assert.match(
    source,
    /value=\{formatBytes\(stats\.value\.bytes_in \|\| 0\)\}/,
    'Dashboard inbound total must render the cumulative bytes_in counter, not the instantaneous bandwidth_in rate'
  )

  assert.match(
    source,
    /value=\{formatBytes\(stats\.value\.bytes_out \|\| 0\)\}/,
    'Dashboard outbound total must render the cumulative bytes_out counter, not the instantaneous bandwidth_out rate'
  )

  assert.doesNotMatch(
    source,
    /value=\{formatBytes\(stats\.value\.bandwidth_(?:in|out)/,
    'Dashboard total cards should not use the short-window bandwidth rate fields'
  )

  assert.doesNotMatch(
    source,
    /B\/s|KB\/s|MB\/s/,
    'Dashboard should not render rate units for cumulative inbound and outbound totals'
  )
})

test('Dashboard formats stream health percentages to two decimals', async () => {
  const source = await readFile(dashboardPath, 'utf8')

  assert.match(
    source,
    /function formatHealthPercent\(health: number\)/,
    'Dashboard should format stream health through a dedicated display helper'
  )

  assert.match(
    source,
    /\.toFixed\(2\)/,
    'Dashboard stream health should be capped to two digits after the decimal point'
  )

  assert.match(
    source,
    /\{formatHealthPercent\(stream\.health\)\}%/,
    'Dashboard should not render raw health values with arbitrary decimal precision'
  )
})
