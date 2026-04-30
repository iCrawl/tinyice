import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

const dashboardPath = fileURLToPath(new URL('./Dashboard.tsx', import.meta.url))

test('Dashboard traffic chart does not derive CSS height from raw listener count', async () => {
  const source = await readFile(dashboardPath, 'utf8')

  assert.doesNotMatch(
    source,
    /stats\.value\.listeners\s*:\s*0\)\s*\*\s*10\)\s*}%/,
    'Listener Traffic bars must be normalized to the chart range instead of multiplying listeners into CSS percentages'
  )
})

test('Dashboard hydrates listener traffic from persisted insights data for the selected range', async () => {
  const source = await readFile(dashboardPath, 'utf8')

  assert.match(
    source,
    /\/admin\/insights\?range=\$\{(?:timeRange\.value|range)\}/,
    'Dashboard must load persisted listener history for the selected range instead of rebuilding the chart from live SSE only'
  )
})

test('Dashboard renders a listener traffic scale alongside the bars', async () => {
  const source = await readFile(dashboardPath, 'utf8')

  assert.match(
    source,
    /getTrafficScaleLabels/,
    'Dashboard must render Y-axis labels so listener traffic bars have an at-a-glance scale'
  )
})

test('Dashboard attaches hover text to listener traffic bars', async () => {
  const source = await readFile(dashboardPath, 'utf8')

  assert.match(
    source,
    /title=\{/,
    'Dashboard must expose per-bar hover text with the exact listener count and time bucket'
  )
})

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

  assert.doesNotMatch(
    source,
    /B\/s|KB\/s|MB\/s/,
    'Dashboard should not render rate units for cumulative inbound and outbound totals'
  )
})
