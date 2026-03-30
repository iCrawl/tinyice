import { signal } from '@preact/signals'
import { useEffect, useRef } from 'preact/hooks'
import { createSSE } from '../../lib/sse'
import { api } from '../../lib/api'
import { StatCard } from '../../components/StatCard'
import type { StatsEvent, StreamEvent } from '../../types'
import {
  alignTrafficBucketStart,
  bucketTrafficSamples,
  collapseTrafficSources,
  formatTrafficBucketLabel,
  getTrafficBarHeights,
  getTrafficScaleLabels,
  type TrafficHistorySample,
  type TrafficRange,
  upsertLiveTrafficSample,
} from './dashboardTraffic'

type DashboardStore = ReturnType<typeof createDashboardStore>
type HistoricalTrafficResponse = Record<string, Array<TrafficHistorySample & {
  bytes_in: number
  bytes_out: number
}>>

function createDashboardStore() {
  const stats = signal<StatsEvent>({
    listeners: 0,
    streams: 0,
    bandwidth: 0,
    bandwidth_in: 0,
    bandwidth_out: 0,
    uptime: 0,
    goroutines: 0,
    memory: 0,
    gc: 0,
  })
  const streams = signal<StreamEvent[]>([])
  const connected = signal(false)
  const timeRange = signal<TrafficRange>('1H')
  const listenerHistory = signal<number[]>([])
  const trafficBucketStartedAt = signal(0)

  return {
    stats,
    streams,
    connected,
    timeRange,
    listenerHistory,
    trafficBucketStartedAt,
  }
}

function useDashboardStore() {
  const storeRef = useRef<DashboardStore | null>(null)
  if (storeRef.current == null) {
    storeRef.current = createDashboardStore()
  }
  return storeRef.current
}

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`
  if (seconds < 86400) {
    const h = Math.floor(seconds / 3600)
    const m = Math.floor((seconds % 3600) / 60)
    return `${h}h ${m}m`
  }
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  return `${d}d ${h}h`
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1073741824) return `${(bytes / 1048576).toFixed(1)} MB`
  return `${(bytes / 1073741824).toFixed(1)} GB`
}

function formatListenerCount(listeners: number): string {
  return `${listeners.toLocaleString()} listener${listeners === 1 ? '' : 's'}`
}

async function loadTrafficHistory(store: DashboardStore, range: TrafficRange, cancelledRef: { current: boolean }) {
  try {
    const historyByMount = await api.get<HistoricalTrafficResponse>(`/admin/insights?range=${range}`)
    if (cancelledRef.current) return

    const now = Date.now()
    const collapsedHistory = collapseTrafficSources(historyByMount)
    const persistedHistory = bucketTrafficSamples(collapsedHistory, range, now)
    const liveHistory = upsertLiveTrafficSample(
      persistedHistory,
      store.stats.value.listeners,
      range,
      alignTrafficBucketStart(range, now),
      now,
    )

    store.listenerHistory.value = liveHistory.history
    store.trafficBucketStartedAt.value = liveHistory.bucketStartedAt
  } catch {
    if (cancelledRef.current) return
    store.trafficBucketStartedAt.value = alignTrafficBucketStart(range)
  }
}

export function Dashboard() {
  const store = useDashboardStore()
  const { stats, streams, connected, timeRange, listenerHistory, trafficBucketStartedAt } = store

  useEffect(() => {
    const cancelledRef = { current: false }
    void loadTrafficHistory(store, timeRange.value, cancelledRef)

    return () => {
      cancelledRef.current = true
    }
  }, [timeRange.value])

  useEffect(() => {
    const sse = createSSE('/admin/events')

    const offStats = sse.on('stats', (data: StatsEvent) => {
      const now = Date.now()
      const nextTraffic = upsertLiveTrafficSample(
        listenerHistory.value,
        data.listeners,
        timeRange.value,
        trafficBucketStartedAt.value,
        now,
      )

      listenerHistory.value = nextTraffic.history
      trafficBucketStartedAt.value = nextTraffic.bucketStartedAt

      stats.value = data
      connected.value = true
    })

    const offStream = sse.on('stream', (data: StreamEvent) => {
      streams.value = [
        ...streams.value.filter((stream) => stream.mount !== data.mount),
        data,
      ].sort((a, b) => a.mount.localeCompare(b.mount))
    })

    return () => {
      offStats()
      offStream()
      sse.close()
    }
  }, [])

  const totalStreams = streams.value.length
  const activeStreams = streams.value.filter((stream) => stream.listeners > 0).length
  const trafficBars = getTrafficBarHeights(listenerHistory.value)
  const paddedTrafficHistory = listenerHistory.value.length >= trafficBars.length
    ? listenerHistory.value.slice(-trafficBars.length)
    : [...Array(trafficBars.length - listenerHistory.value.length).fill(0), ...listenerHistory.value]
  const trafficScaleLabels = getTrafficScaleLabels(paddedTrafficHistory)
  const trafficRangeEnd = Date.now()
  const hasTrafficData = listenerHistory.value.some((listenersCount) => listenersCount > 0)

  return (
    <div class="p-7 max-w-[1400px]">
      <div class="flex items-center justify-between mb-6">
        <div>
          <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">
            DASHBOARD
          </div>
          <h1 class="text-xl font-bold text-text-primary">System Overview</h1>
        </div>
        <div class="flex items-center gap-2">
          <span
            class="w-2 h-2 rounded-full"
            style={{
              backgroundColor: connected.value ? 'var(--color-live)' : 'var(--color-danger)',
              animation: connected.value ? 'pulse-glow 2s ease-in-out infinite' : 'none',
            }}
          />
          <span class="font-mono text-[10px] tracking-widest text-text-tertiary uppercase">
            {connected.value ? 'ALL SYSTEMS OK' : 'CONNECTING...'}
          </span>
        </div>
      </div>

      <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4 mb-6">
        <StatCard
          label="Listeners"
          value={stats.value.listeners}
          subtitle="connected now"
          gauge={Math.min(100, stats.value.listeners)}
        />
        <StatCard
          label="Streams"
          value={`${activeStreams} / ${totalStreams}`}
          subtitle="active / total"
        />
        <StatCard
          label="Inbound Total"
          value={formatBytes(stats.value.bandwidth_in || 0)}
          subtitle="received since start"
        />
        <StatCard
          label="Outbound Total"
          value={formatBytes(stats.value.bandwidth_out || stats.value.bandwidth || 0)}
          subtitle="sent since start"
        />
        <StatCard
          label="Uptime"
          value={formatUptime(stats.value.uptime)}
          subtitle="since last restart"
        />
      </div>

      <div class="rounded-lg border border-border bg-surface-raised p-4 mb-6">
        <div class="flex items-center justify-between mb-4">
          <span class="font-mono text-[10px] tracking-widest uppercase text-text-tertiary">
            Listener Traffic
          </span>
          <div class="flex gap-1">
            {(['1H', '24H', '7D'] as const).map((range) => (
              <button
                key={range}
                onClick={() => { timeRange.value = range }}
                class={`
                  px-2 py-1 rounded font-mono text-[10px] tracking-wider transition-colors
                  ${
                    timeRange.value === range
                      ? 'bg-accent/15 text-accent'
                      : 'text-text-tertiary hover:text-text-secondary hover:bg-surface-hover'
                  }
                `}
              >
                {range}
              </button>
            ))}
          </div>
        </div>
        <div class="flex gap-3">
          {hasTrafficData ? (
            <div class="h-32 w-12 shrink-0 flex flex-col justify-between text-right font-mono text-[9px] text-text-tertiary">
              {trafficScaleLabels.map((label) => (
                <span key={label}>{label.toLocaleString()}</span>
              ))}
            </div>
          ) : null}
          <div class="relative h-32 flex-1">
            {hasTrafficData ? (
              <>
                <div class="pointer-events-none absolute inset-0 flex flex-col justify-between">
                  {trafficScaleLabels.map((label, i) => (
                    <div
                      key={`${label}-${i}`}
                      class={i === trafficScaleLabels.length - 1 ? 'border-t border-border/50' : 'border-t border-border/30'}
                    />
                  ))}
                </div>
                <div class="relative flex h-full items-end gap-px">
                  {trafficBars.map((height, i) => {
                    const listenersCount = paddedTrafficHistory[i]
                    const hoverLabel = `${formatListenerCount(listenersCount)}\n${formatTrafficBucketLabel(i, timeRange.value, trafficRangeEnd, trafficBars.length)}`

                    return (
                      <div
                        key={i}
                        class="group flex-1 h-full flex items-end"
                        title={hoverLabel}
                        aria-label={hoverLabel}
                      >
                        <div
                          class="w-full rounded-t bg-accent/20 transition-[height,background-color] duration-200 group-hover:bg-accent/35"
                          style={{ height: `${height}%` }}
                        />
                      </div>
                    )
                  })}
                </div>
              </>
            ) : (
              <div class="flex h-full items-center justify-center text-text-tertiary text-xs font-mono">
                No listener data yet
              </div>
            )}
          </div>
        </div>
        <div class="flex justify-between mt-2">
          <span class="font-mono text-[9px] text-text-tertiary">
            {timeRange.value === '1H' ? '60m ago' : timeRange.value === '24H' ? '24h ago' : '7d ago'}
          </span>
          <span class="font-mono text-[9px] text-text-tertiary">now</span>
        </div>
      </div>

      <div class="admin-table-shell bg-surface-raised">
        <div class="px-4 py-3 border-b border-border">
          <span class="font-mono text-[10px] tracking-widest uppercase text-text-tertiary">
            Active Streams
          </span>
        </div>
        {streams.value.length === 0 ? (
          <div class="px-4 py-8 text-center text-text-tertiary text-sm">
            No streams connected
          </div>
        ) : (
          <div class="admin-table-scroll">
            <table class="w-full min-w-[700px]">
              <thead>
                <tr class="text-left text-text-tertiary font-mono text-[10px] tracking-wider uppercase border-b border-border">
                  <th class="px-4 py-2 font-normal">Status</th>
                  <th class="px-4 py-2 font-normal">Mount</th>
                  <th class="px-4 py-2 font-normal">Format</th>
                  <th class="px-4 py-2 font-normal">Listeners</th>
                  <th class="px-4 py-2 font-normal">Health</th>
                </tr>
              </thead>
              <tbody>
                {streams.value.map((stream) => (
                  <tr
                    key={stream.mount}
                    class="border-b border-border last:border-b-0 hover:bg-surface-hover transition-colors"
                  >
                    <td class="px-4 py-3">
                      <span
                        class="w-2 h-2 rounded-full inline-block"
                        style={{
                          backgroundColor:
                            stream.listeners > 0
                              ? 'var(--color-live)'
                              : 'var(--color-text-tertiary)',
                        }}
                      />
                    </td>
                    <td class="px-4 py-3">
                      <span class="font-mono font-bold text-sm text-text-primary">
                        {stream.mount}
                      </span>
                      {stream.title && (
                        <div class="text-xs text-text-secondary mt-0.5">
                          {stream.artist ? `${stream.artist} - ${stream.title}` : stream.title}
                        </div>
                      )}
                    </td>
                    <td class="px-4 py-3">
                      <span class="font-mono text-xs text-text-secondary uppercase">
                        {stream.format}
                      </span>
                      {stream.bitrate > 0 && (
                        <span class="text-text-tertiary text-xs ml-1">
                          {stream.bitrate}k
                        </span>
                      )}
                    </td>
                    <td class="px-4 py-3">
                      <span class="font-mono text-sm text-text-primary">
                        {stream.listeners}
                      </span>
                    </td>
                    <td class="px-4 py-3">
                      <div class="flex items-center gap-2">
                        <div class="h-1 w-16 rounded-full bg-surface-overlay overflow-hidden">
                          <div
                            class="h-full rounded-full transition-all duration-500"
                            style={{
                              width: `${Math.min(100, Math.max(0, stream.health))}%`,
                              backgroundColor:
                                stream.health >= 80
                                  ? 'var(--color-live)'
                                  : stream.health >= 50
                                    ? 'var(--color-accent)'
                                    : 'var(--color-danger)',
                            }}
                          />
                        </div>
                        <span class="font-mono text-[10px] text-text-tertiary">
                          {stream.health}%
                        </span>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <div class="flex gap-6 mt-4 text-text-tertiary font-mono text-[10px] tracking-wider">
        <span>MEM {(stats.value.memory / 1048576).toFixed(1)} MB</span>
        <span>GR {stats.value.goroutines}</span>
        <span>GC {stats.value.gc}</span>
      </div>
    </div>
  )
}
