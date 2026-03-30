import type { StatsEvent, StreamEvent, AutoDJEvent, StreamInfo } from '../types'

type SSEEventMap = {
  stats: StatsEvent
  stream: StreamEvent
  autodj: AutoDJEvent
  streams: StreamInfo[]
  metadata: { mount: string; title: string; artist: string; started_at: string }
}

type SSECallback<K extends keyof SSEEventMap> = (data: SSEEventMap[K]) => void

export function createSSE(url: string) {
  let source: EventSource | null = null
  const listeners = new Map<string, Set<Function>>()
  const namedHandlers = new Map<string, (e: Event) => void>()
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectDelay = 1000

  function dispatch(event: string, data: unknown) {
    listeners.get(event)?.forEach((cb) => cb(data))
  }

  function ensureNamedListener(event: string) {
    if (!source || event === 'message' || namedHandlers.has(event)) return

    const handler = (e: Event) => {
      try {
        dispatch(event, JSON.parse((e as MessageEvent).data))
      } catch {}
    }

    namedHandlers.set(event, handler)
    source.addEventListener(event, handler)
  }

  function connect() {
    source = new EventSource(url)
    namedHandlers.clear()
    source.onopen = () => { reconnectDelay = 1000 }
    source.onerror = () => {
      source?.close()
      reconnectTimer = setTimeout(connect, reconnectDelay)
      reconnectDelay = Math.min(reconnectDelay * 2, 30000)
    }
    // Legacy untyped messages
    source.onmessage = (e) => {
      try {
        dispatch('message', JSON.parse(e.data))
      } catch {}
    }
    for (const type of listeners.keys()) {
      ensureNamedListener(type)
    }
  }

  function on<K extends keyof SSEEventMap>(event: K, callback: SSECallback<K>): () => void
  function on(event: 'message', callback: (data: unknown) => void): () => void
  function on(event: string, callback: Function): () => void {
    if (!listeners.has(event)) listeners.set(event, new Set())
    listeners.get(event)!.add(callback)
    ensureNamedListener(event)
    return () => { listeners.get(event)?.delete(callback) }
  }

  function close() {
    source?.close()
    if (reconnectTimer) clearTimeout(reconnectTimer)
  }

  connect()
  return { on, close }
}
