import type { StatsEvent } from '../../types'

export const TRAFFIC_BAR_COUNT = 48
export const TRAFFIC_SAMPLE_INTERVAL_MS = 30_000
const MIN_BAR_HEIGHT_PERCENT = 4

export function pushTrafficSample(history: number[], listeners: number, limit = TRAFFIC_BAR_COUNT): number[] {
  const safeListeners = Number.isFinite(listeners) ? Math.max(0, listeners) : 0
  return [...history, safeListeners].slice(-limit)
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
