import type { StatsEvent } from '../../types'

export const TRAFFIC_BAR_COUNT = 48
export const TRAFFIC_SAMPLE_INTERVAL_MS = 30_000
const MIN_BAR_HEIGHT_PERCENT = 4
const MINUTE_MS = 60_000

export type TrafficRange = '1H' | '24H' | '7D'

export interface TrafficHistorySample {
  timestamp: string | number | Date
  listeners: number
}

const TRAFFIC_RANGE_DURATION_MS: Record<TrafficRange, number> = {
  '1H': 60 * MINUTE_MS,
  '24H': 24 * 60 * MINUTE_MS,
  '7D': 7 * 24 * 60 * MINUTE_MS,
}

export function pushTrafficSample(history: number[], listeners: number, limit = TRAFFIC_BAR_COUNT): number[] {
  const safeListeners = Number.isFinite(listeners) ? Math.max(0, listeners) : 0
  return [...history, safeListeners].slice(-limit)
}

function normalizeListeners(listeners: number): number {
  return Number.isFinite(listeners) ? Math.max(0, listeners) : 0
}

function normalizeTimestamp(timestamp: TrafficHistorySample['timestamp']): number {
  if (timestamp instanceof Date) return timestamp.getTime()
  if (typeof timestamp === 'number') return timestamp
  const parsed = Date.parse(timestamp)
  return Number.isFinite(parsed) ? parsed : Number.NaN
}

export function getTrafficRangeDurationMs(range: TrafficRange): number {
  return TRAFFIC_RANGE_DURATION_MS[range]
}

export function getTrafficBucketSizeMs(range: TrafficRange, limit = TRAFFIC_BAR_COUNT): number {
  return Math.max(1, Math.floor(getTrafficRangeDurationMs(range) / limit))
}

export function alignTrafficBucketStart(range: TrafficRange, now = Date.now(), limit = TRAFFIC_BAR_COUNT): number {
  const bucketSizeMs = getTrafficBucketSizeMs(range, limit)
  return Math.floor(now / bucketSizeMs) * bucketSizeMs
}

export function collapseTrafficSources(historyByMount: Record<string, TrafficHistorySample[]>): TrafficHistorySample[] {
  const totals = new Map<number, number>()

  for (const samples of Object.values(historyByMount)) {
    for (const sample of samples) {
      const timestamp = normalizeTimestamp(sample.timestamp)
      if (!Number.isFinite(timestamp)) continue

      const minuteSlot = Math.floor(timestamp / MINUTE_MS) * MINUTE_MS
      totals.set(minuteSlot, (totals.get(minuteSlot) ?? 0) + normalizeListeners(sample.listeners))
    }
  }

  return Array.from(totals.entries())
    .sort((a, b) => a[0] - b[0])
    .map(([timestamp, listeners]) => ({ timestamp, listeners }))
}

export function bucketTrafficSamples(
  samples: TrafficHistorySample[],
  range: TrafficRange,
  now = Date.now(),
  limit = TRAFFIC_BAR_COUNT,
): number[] {
  const bucketSizeMs = getTrafficBucketSizeMs(range, limit)
  const rangeStart = now - getTrafficRangeDurationMs(range)
  const buckets = Array.from({ length: limit }, () => 0)

  for (const sample of samples) {
    const timestamp = normalizeTimestamp(sample.timestamp)
    if (!Number.isFinite(timestamp) || timestamp < rangeStart || timestamp > now) continue

    const bucketIndex = Math.min(limit - 1, Math.max(0, Math.floor((timestamp - rangeStart) / bucketSizeMs)))
    buckets[bucketIndex] = Math.max(buckets[bucketIndex], normalizeListeners(sample.listeners))
  }

  return buckets
}

export function upsertLiveTrafficSample(
  history: number[],
  listeners: number,
  range: TrafficRange,
  bucketStartedAt: number,
  now = Date.now(),
  limit = TRAFFIC_BAR_COUNT,
): { history: number[]; bucketStartedAt: number } {
  const bucketSizeMs = getTrafficBucketSizeMs(range, limit)
  const alignedBucketStart = alignTrafficBucketStart(range, now, limit)
  const safeListeners = normalizeListeners(listeners)
  const paddedHistory = history.length >= limit
    ? history.slice(-limit)
    : [...Array(limit - history.length).fill(0), ...history]

  if (bucketStartedAt <= 0) {
    return {
      history: [...paddedHistory.slice(0, -1), safeListeners],
      bucketStartedAt: alignedBucketStart,
    }
  }

  const elapsedBuckets = Math.max(0, Math.floor((alignedBucketStart - bucketStartedAt) / bucketSizeMs))
  const nextHistory = elapsedBuckets >= limit
    ? Array.from({ length: limit }, () => 0)
    : elapsedBuckets > 0
      ? [...paddedHistory.slice(elapsedBuckets), ...Array(elapsedBuckets).fill(0)]
      : [...paddedHistory]

  nextHistory[nextHistory.length - 1] = safeListeners

  return {
    history: nextHistory,
    bucketStartedAt: alignedBucketStart,
  }
}

export function shouldPushTrafficSample(
  previous: StatsEvent | null,
  next: StatsEvent,
  lastSampleAt: number,
  now = Date.now(),
  minIntervalMs = TRAFFIC_SAMPLE_INTERVAL_MS,
): boolean {
  if (!previous || lastSampleAt <= 0) return true

  if (previous.listeners !== next.listeners) return true

  return now - lastSampleAt >= minIntervalMs
}

export function getTrafficBarHeights(history: number[], limit = TRAFFIC_BAR_COUNT): number[] {
  const recent = history.slice(-limit)
  const padded = recent.length >= limit
    ? recent
    : [...Array(limit - recent.length).fill(0), ...recent]
  const peak = Math.max(...padded, 1)

  return padded.map((value) => {
    if (value <= 0) return 0
    return Math.max(MIN_BAR_HEIGHT_PERCENT, Math.round((value / peak) * 100))
  })
}

export function getTrafficScaleLabels(history: number[]): number[] {
  const peak = history.reduce((max, value) => Math.max(max, normalizeListeners(value)), 0)
  return Array.from(new Set([peak, Math.round(peak / 2), 0]))
}

function formatTrafficTime(timestamp: number, timeZone?: string): string {
  return new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
    ...(timeZone ? { timeZone } : {}),
  }).format(new Date(timestamp))
}

function formatTrafficDate(timestamp: number, timeZone?: string): string {
  return new Intl.DateTimeFormat('en-GB', {
    month: 'short',
    day: 'numeric',
    ...(timeZone ? { timeZone } : {}),
  }).format(new Date(timestamp))
}

export function formatTrafficBucketLabel(
  bucketIndex: number,
  range: TrafficRange,
  now = Date.now(),
  limit = TRAFFIC_BAR_COUNT,
  timeZone?: string,
): string {
  const bucketSizeMs = getTrafficBucketSizeMs(range, limit)
  const rangeStart = now - getTrafficRangeDurationMs(range)
  const bucketStart = rangeStart + bucketIndex * bucketSizeMs
  const bucketEnd = bucketStart + bucketSizeMs
  const includeDate = range === '7D' || formatTrafficDate(bucketStart, timeZone) !== formatTrafficDate(bucketEnd, timeZone)
  const startLabel = includeDate
    ? `${formatTrafficDate(bucketStart, timeZone)} ${formatTrafficTime(bucketStart, timeZone)}`
    : formatTrafficTime(bucketStart, timeZone)
  const endLabel = includeDate
    ? `${formatTrafficDate(bucketEnd, timeZone)} ${formatTrafficTime(bucketEnd, timeZone)}`
    : formatTrafficTime(bucketEnd, timeZone)

  return `${startLabel} - ${endLabel}`
}
