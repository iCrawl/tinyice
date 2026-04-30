import test from 'node:test'
import assert from 'node:assert/strict'

const {
  bucketTrafficSamples,
  formatTrafficBucketLabel,
  getTrafficBarHeights,
  getTrafficScaleLabels,
  pushTrafficSample,
  shouldPushTrafficSample,
  TRAFFIC_BAR_COUNT,
  TRAFFIC_SAMPLE_INTERVAL_MS,
  upsertLiveTrafficSample,
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

test('getTrafficScaleLabels derives zero, midpoint, and peak labels from listener history', () => {
  const labels = getTrafficScaleLabels([0, 3, 18, 24, 7])

  assert.deepEqual(labels, [24, 12, 0])
})

test('formatTrafficBucketLabel describes the bucket time window for hover text', () => {
  const now = Date.UTC(2026, 2, 26, 12, 0, 0)
  const label = formatTrafficBucketLabel(TRAFFIC_BAR_COUNT - 1, '24H', now, TRAFFIC_BAR_COUNT, 'UTC')

  assert.match(label, /11:30/)
  assert.match(label, /12:00/)
})

test('bucketTrafficSamples aggregates persisted listener history into the selected range buckets', () => {
  const now = Date.UTC(2026, 2, 26, 12, 0, 0)
  const bucketMs = (24 * 60 * 60 * 1000) / TRAFFIC_BAR_COUNT
  const samples = [
    {
      timestamp: new Date(now - bucketMs * 3 + 1_000).toISOString(),
      listeners: 7,
    },
    {
      timestamp: new Date(now - bucketMs * 3 + 5_000).toISOString(),
      listeners: 11,
    },
    {
      timestamp: new Date(now - bucketMs + 2_000).toISOString(),
      listeners: 5,
    },
  ]

  const buckets = bucketTrafficSamples(samples, '24H', now)

  assert.equal(buckets.length, TRAFFIC_BAR_COUNT)
  assert.equal(buckets.at(-3), 11)
  assert.equal(buckets.at(-1), 5)
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

test('upsertLiveTrafficSample updates the active bucket instead of appending before the bucket rolls over', () => {
  const now = Date.UTC(2026, 2, 26, 12, 0, 0)
  const bucketSizeMs = (60 * 60 * 1000) / TRAFFIC_BAR_COUNT
  const history = Array.from({ length: TRAFFIC_BAR_COUNT }, () => 0)

  const first = upsertLiveTrafficSample(history, 8, '1H', now - 5_000, now)
  const second = upsertLiveTrafficSample(first.history, 13, '1H', first.bucketStartedAt, now + 2_000)

  assert.equal(first.history.length, TRAFFIC_BAR_COUNT)
  assert.equal(first.history.at(-1), 8)
  assert.equal(second.history.length, TRAFFIC_BAR_COUNT)
  assert.equal(second.history.at(-1), 13)
  assert.equal(second.history.at(-2), 0)
  assert.equal(second.bucketStartedAt, first.bucketStartedAt)

  const rolled = upsertLiveTrafficSample(second.history, 21, '1H', second.bucketStartedAt, now + bucketSizeMs + 1)
  assert.equal(rolled.history.at(-2), 13)
  assert.equal(rolled.history.at(-1), 21)
})
