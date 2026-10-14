import { signal } from '@preact/signals'
import { useEffect, useMemo } from 'preact/hooks'
import { api } from '../../lib/api'
import { describeListenerMove, reconcilePendingMoves } from '../../lib/listenerMoves'
import { reportError } from '../../lib/toast'
import type { AdminStream, ListenerInfo, ListenerMove } from '../../types'

function createListenersStore() {
  return {
    listeners: signal<ListenerInfo[]>([]),
    mounts: signal<string[]>([]),
    loading: signal(true),
    error: signal(''),
    loadError: signal(''),
    filter: signal(new URLSearchParams(window.location.search).get('mount') || ''),
    inspecting: signal<ListenerInfo | null>(null),
    target: signal(''),
    busy: signal(false),
    pending: signal<Record<string, ListenerMove>>({}),
  }
}

export function Listeners() {
  const store = useMemo(createListenersStore, [])
  const { listeners, mounts, loading, error, loadError, filter, inspecting, target, busy, pending } = store

  useEffect(() => {
    let active = true
    let fetching = false
    async function refresh() {
      if (fetching) return
      fetching = true
      try {
        const [next, streams] = await Promise.all([
          api.get<ListenerInfo[]>('/api/listeners'),
          api.get<AdminStream[]>('/api/streams'),
        ])
        if (!active) return
        listeners.value = next
        mounts.value = streams.map((stream) => stream.mount)
        loadError.value = ''
        pending.value = reconcilePendingMoves(pending.value, next)
        if (inspecting.value) {
          inspecting.value = next.find((listener) => listener.id === inspecting.value?.id) || null
        }
      } catch (e) {
        if (active) loadError.value = e instanceof Error ? e.message : 'Could not load listeners'
      } finally {
        fetching = false
        if (active) loading.value = false
      }
    }
    void refresh()
    const timer = setInterval(() => { void refresh() }, 2000)
    return () => { active = false; clearInterval(timer) }
  }, [])

  async function disconnect(listener: ListenerInfo) {
    busy.value = true
    error.value = ''
    try {
      await api.post('/api/listeners/disconnect', { id: listener.id })
    } catch (e) {
      reportError(e, 'Could not disconnect listener')
    } finally {
      busy.value = false
    }
  }

  async function move(listener: ListenerInfo) {
    busy.value = true
    error.value = ''
    try {
      const result = await api.post<ListenerMove>('/api/listeners/move', { id: listener.id, target_mount: target.value })
      pending.value = { ...pending.value, [listener.id]: result }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Could not move listener'
    } finally {
      busy.value = false
    }
  }

  const rows = listeners.value.filter((listener) => !filter.value || listener.current_mount === filter.value)
  const selected = inspecting.value
  return (
    <div class="p-7">
      <h1 class="text-xl font-bold mb-2">Live listeners</h1>
      <p class="text-sm text-text-secondary mb-6">Managed HTTP and WebRTC connections. HLS viewers are aggregate metrics on the Streams page.</p>
      <label class="text-sm text-text-secondary">Current mount
        <select value={filter.value} onChange={(e) => { filter.value = e.currentTarget.value }} class="ml-3 border border-border rounded-lg bg-surface-base p-2">
          <option value="">All mounts</option>
          {mounts.value.map((mount) => <option key={mount} value={mount}>{mount}</option>)}
        </select>
      </label>
      {loadError.value && <p role="alert" class="text-danger my-4">{loadError.value}</p>}
      <div class="mt-5 overflow-x-auto border border-border rounded-xl">
        <table class="w-full text-sm text-left">
          <thead><tr class="border-b border-border">{['Protocol', 'Current mount', 'Address', 'Connected', 'Move', 'Actions'].map((label) => <th key={label} class="p-3 text-text-tertiary">{label}</th>)}</tr></thead>
          <tbody>{loading.value ? <tr><td colSpan={6} class="p-6">Loading...</td></tr> : rows.length === 0 ? <tr><td colSpan={6} class="p-6">No managed listeners connected.</td></tr> : rows.map((listener) => <tr key={listener.id} class="border-b border-border">
            <td class="p-3 uppercase">{listener.protocol}</td>
            <td class="p-3 font-mono">{listener.current_mount}<div class="text-xs text-text-tertiary">Requested {listener.requested_mount}</div></td>
            <td class="p-3">{listener.remote_addr}</td>
            <td class="p-3">{Math.floor(listener.duration_seconds)}s</td>
            <td class={`p-3 ${listener.move_status === 'failed' ? 'text-danger' : 'text-text-secondary'}`} aria-live="polite">{describeListenerMove(listener, pending.value[listener.id])}</td>
            <td class="p-3"><button onClick={() => { inspecting.value = listener; target.value = ''; error.value = '' }} class="border border-border rounded-lg px-3 py-2">Inspect</button></td>
          </tr>)}</tbody>
        </table>
      </div>
      {selected && <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
        <div role="dialog" aria-modal="true" aria-label="Inspect listener" class="bg-surface-overlay border border-border rounded-xl p-6 max-w-lg w-full mx-4">
          <h2 class="font-bold text-lg mb-4">Listener {selected.id}</h2>
          <dl class="text-sm space-y-2 break-words">
            <dt class="text-text-tertiary">Protocol and address</dt><dd>{selected.protocol} · {selected.remote_addr}</dd>
            <dt class="text-text-tertiary">Current mount</dt><dd>{selected.current_mount}</dd>
            <dt class="text-text-tertiary">Requested mount</dt><dd>{selected.requested_mount}</dd>
            <dt class="text-text-tertiary">User agent</dt><dd>{selected.user_agent || 'Unknown'}</dd>
            <dt class="text-text-tertiary">Connected</dt><dd>{new Date(selected.connected_at * 1000).toLocaleString()}</dd>
          </dl>
          <p class="text-sm mt-4" aria-live="polite">{describeListenerMove(selected, pending.value[selected.id])}</p>
          {selected.protocol === 'http' ? <label class="block text-sm mt-4">Move to mount
            <select value={target.value} onChange={(e) => { target.value = e.currentTarget.value }} class="mt-1 w-full border border-border rounded-lg p-2 bg-surface-base">
              <option value="">Choose destination</option>
              {mounts.value.filter((mount) => mount !== selected.current_mount).map((mount) => <option key={mount} value={mount}>{mount}</option>)}
            </select>
          </label> : <p class="text-sm text-text-tertiary mt-4">WebRTC connections cannot move between mounts. Disconnect and reconnect to the destination.</p>}
          {error.value && <p role="alert" class="text-danger mt-4">{error.value}</p>}
          <div class="flex flex-wrap gap-2 mt-5">
            {selected.protocol === 'http' && <button disabled={busy.value || !target.value || target.value === selected.current_mount || !!pending.value[selected.id] || selected.move_status === 'pending'} onClick={() => move(selected)} class="border border-border rounded-lg px-3 py-2 disabled:opacity-50">Move</button>}
            <button disabled={busy.value} onClick={() => disconnect(selected)} class="border border-danger text-danger rounded-lg px-3 py-2 disabled:opacity-50">Disconnect</button>
            <button onClick={() => { inspecting.value = null }} class="ml-auto border border-border rounded-lg px-3 py-2">Close</button>
          </div>
        </div>
      </div>}
    </div>
  )
}
