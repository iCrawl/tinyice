import test from 'node:test'
import assert from 'node:assert/strict'

const {
  getTrafficBarHeights,
  pushTrafficSample,
  shouldPushTrafficSample,
  TRAFFIC_BAR_COUNT,
  TRAFFIC_SAMPLE_INTERVAL_MS,
} =
  await import('./dashboardTraffic.ts')

test('pushTrafficSample keeps a bounded traffic history', () => {
  const history = Array.from({ length: TRAFFIC_BAR_COUNT }, (_, i) => i + 1)
  const next = pushTrafficSample(history, 999)

  assert.equal(next.length, TRAFFIC_BAR_COUNT)
  assert.equal(next[0], 2)
  assert.equal(next.at(-1), 999)
})

test('getTrafficBarHeights normalizes bars into the chart range', () => {
  const heights = getTrafficBarHeights([0, 52])

  assert.equal(heights.length, TRAFFIC_BAR_COUNT)
  assert.equal(heights.at(-1), 100)
  assert.ok(heights.every((height) => height >= 0 && height <= 100))
})

test('shouldPushTrafficSample ignores repeated heartbeat-only stats updates', () => {
  const previous = {
    listeners: 12,
    streams: 2,
    bandwidth: 4096,
    bandwidth_in: 1024,
    bandwidth_out: 4096,
    uptime: 100,
    goroutines: 20,
    memory: 1024,
    gc: 1,
  }
  const next = {
    ...previous,
    uptime: previous.uptime + 1,
  }

  assert.equal(
    shouldPushTrafficSample(previous, next, 10_000, 10_000 + TRAFFIC_SAMPLE_INTERVAL_MS - 1),
    false,
  )
})

test('shouldPushTrafficSample ignores bandwidth-only changes before the minimum interval', () => {
  const previous = {
    listeners: 12,
    streams: 2,
    bandwidth: 4096,
    bandwidth_in: 1024,
    bandwidth_out: 4096,
    uptime: 100,
    goroutines: 20,
    memory: 1024,
    gc: 1,
  }
  const next = {
    ...previous,
    bandwidth: 6144,
    bandwidth_in: 2048,
    bandwidth_out: 6144,
    uptime: previous.uptime + 1,
  }

  assert.equal(
    shouldPushTrafficSample(previous, next, 10_000, 10_001),
    false,
  )
})

test('shouldPushTrafficSample allows a fresh sample after the minimum interval', () => {
  const previous = {
    listeners: 12,
    streams: 2,
    bandwidth: 4096,
    bandwidth_in: 1024,
    bandwidth_out: 4096,
    uptime: 100,
    goroutines: 20,
    memory: 1024,
    gc: 1,
  }
  const next = {
    ...previous,
    uptime: previous.uptime + 1,
  }

  assert.equal(
    shouldPushTrafficSample(previous, next, 10_000, 10_000 + TRAFFIC_SAMPLE_INTERVAL_MS),
    true,
  )
})

test('shouldPushTrafficSample allows an immediate sample when listener traffic changes', () => {
  const previous = {
    listeners: 12,
    streams: 2,
    bandwidth: 4096,
    bandwidth_in: 1024,
    bandwidth_out: 4096,
    uptime: 100,
    goroutines: 20,
    memory: 1024,
    gc: 1,
  }
  const next = {
    ...previous,
    listeners: 18,
    uptime: previous.uptime + 1,
  }

  assert.equal(
    shouldPushTrafficSample(previous, next, 10_000, 10_001),
    true,
  )
})
