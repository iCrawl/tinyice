import type { AdminData, ListenerInfo } from '../../types'
import { signal } from '@preact/signals'
import { useEffect, useRef } from 'preact/hooks'
import { api } from '../../lib/api'

type ListenersStore = ReturnType<typeof createListenersStore>

const adminData = (window.__TINYICE__ ?? {}) as Partial<AdminData>

function createListenersStore() {
  return {
    listeners: signal<ListenerInfo[]>([]),
    loading: signal(true),
    filterMount: signal(''),
    selectedIds: signal<Record<string, boolean>>({}),
    showMoveModal: signal(false),
    bulkTargetMount: signal(''),
  }
}

function useListenersStore() {
  const storeRef = useRef<ListenersStore | null>(null)
  if (storeRef.current == null) storeRef.current = createListenersStore()
  return storeRef.current
}

async function load(store: ListenersStore) {
  store.loading.value = true
  try {
    const next = await api.get<ListenerInfo[]>('/api/listeners')
    const knownIds = new Set(next.map((listener) => listener.id))
    store.listeners.value = next
    store.selectedIds.value = Object.fromEntries(
      Object.entries(store.selectedIds.value).filter(([id, selected]) => selected && knownIds.has(id)),
    )
  } finally {
    store.loading.value = false
  }
}

async function disconnectListener(store: ListenersStore, id: string) {
  await api.post('/api/listeners/disconnect', { id })
  await load(store)
}

async function moveSelectedListeners(store: ListenersStore, ids: string[]) {
  const targetMount = store.bulkTargetMount.value
  try {
    for (const id of ids) {
      await api.post('/api/listeners/move', { id, target_mount: targetMount })
    }
  } finally {
    await load(store)
  }
  store.selectedIds.value = {}
  store.showMoveModal.value = false
  store.bulkTargetMount.value = ''
}

function availableMounts(store: ListenersStore): string[] {
  const mounts = Array.isArray(adminData.mounts) ? [...adminData.mounts] : []
  const seen = new Set(mounts)
  for (const listener of store.listeners.value) {
    for (const mount of [listener.current_mount, listener.requested_mount]) {
      if (mount && !seen.has(mount)) {
        seen.add(mount)
        mounts.push(mount)
      }
    }
  }
  return mounts
}

function filteredListeners(store: ListenersStore): ListenerInfo[] {
  if (!store.filterMount.value) return store.listeners.value
  return store.listeners.value.filter((listener) => listener.current_mount === store.filterMount.value)
}

function selectedListenerIds(store: ListenersStore): string[] {
  return Object.entries(store.selectedIds.value)
    .filter(([, selected]) => selected)
    .map(([id]) => id)
}

function formatDuration(totalSeconds: number): string {
  if (totalSeconds < 60) return `${totalSeconds}s`
  if (totalSeconds < 3600) return `${Math.floor(totalSeconds / 60)}m`
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  return `${hours}h ${minutes}m`
}

export function Listeners() {
  const store = useListenersStore()
  const mounts = availableMounts(store)
  const rows = filteredListeners(store)
  const selectedIds = selectedListenerIds(store)
  const allVisibleSelected = rows.length > 0 && rows.every((listener) => store.selectedIds.value[listener.id])

  useEffect(() => {
    void load(store)
    const timer = window.setInterval(() => { void load(store) }, 5000)
    return () => window.clearInterval(timer)
  }, [])

  return (
    <div class="p-7">
      <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">PLAYBACK</div>
      <div class="flex items-start justify-between mb-6 gap-4">
        <div>
          <h1 class="text-xl font-bold text-text-primary">Listeners</h1>
          <p class="text-sm text-text-tertiary mt-1">Active HTTP and WebRTC playback clients.</p>
        </div>
        <select
          value={store.filterMount.value}
          onChange={(e) => {
            store.filterMount.value = (e.target as HTMLSelectElement).value
            store.selectedIds.value = {}
          }}
          class="min-w-52 bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
        >
          <option value="">All ({store.listeners.value.length})</option>
          {mounts.map((mount) => (
            <option key={mount} value={mount}>{mount}</option>
          ))}
        </select>
      </div>

      <div class="flex items-center justify-between mb-4 gap-3">
        <span class="font-mono text-[11px] tracking-[1px] uppercase text-text-tertiary">
          {selectedIds.length === 0 ? `${rows.length} active` : `${selectedIds.length} selected`}
        </span>
        <button
          onClick={() => {
            store.bulkTargetMount.value = mounts[0] || ''
            store.showMoveModal.value = true
          }}
          disabled={selectedIds.length === 0 || mounts.length === 0}
          class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg disabled:opacity-50 disabled:cursor-not-allowed"
        >
          Move Selected
        </button>
      </div>

      <div class="admin-table-shell">
        <div class="admin-table-scroll">
          <table class="w-full min-w-[1080px]">
            <thead>
              <tr class="border-b border-border">
                <th class="px-4 py-3 w-12">
                  <input
                    type="checkbox"
                    checked={allVisibleSelected}
                    disabled={rows.length === 0}
                    onChange={(e) => {
                      const checked = (e.target as HTMLInputElement).checked
                      store.selectedIds.value = checked
                        ? { ...store.selectedIds.value, ...Object.fromEntries(rows.map((listener) => [listener.id, true])) }
                        : {}
                    }}
                    aria-label="Select all visible listeners"
                  />
                </th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Protocol</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Current</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Requested</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Client</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Connected</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-right px-4 py-3">Actions</th>
              </tr>
            </thead>
            <tbody>
              {store.loading.value ? (
                <tr><td colSpan={7} class="px-4 py-8 text-center text-text-tertiary text-sm">Loading...</td></tr>
              ) : rows.length === 0 ? (
                <tr><td colSpan={7} class="px-4 py-8 text-center text-text-tertiary text-sm">No active listeners</td></tr>
              ) : rows.map((listener) => (
                <tr key={listener.id} class="border-b border-[rgba(255,255,255,0.03)]">
                  <td class="px-4 py-3.5">
                    <input
                      type="checkbox"
                      checked={Boolean(store.selectedIds.value[listener.id])}
                      onChange={(e) => {
                        const checked = (e.target as HTMLInputElement).checked
                        const next = { ...store.selectedIds.value }
                        if (checked) next[listener.id] = true
                        else delete next[listener.id]
                        store.selectedIds.value = next
                      }}
                      aria-label={`Select listener ${listener.id}`}
                    />
                  </td>
                  <td class="px-4 py-3.5">
                    <span class="inline-block font-mono text-[10px] tracking-[1px] uppercase px-2 py-0.5 rounded bg-surface-overlay text-text-secondary">{listener.protocol}</span>
                  </td>
                  <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{listener.current_mount}</td>
                  <td class="px-4 py-3.5 font-mono text-sm text-text-tertiary">{listener.requested_mount}</td>
                  <td class="px-4 py-3.5">
                    <div class="text-sm text-text-primary">{listener.remote_addr || 'unknown'}</div>
                    <div class="text-xs text-text-tertiary truncate max-w-[320px]">{listener.user_agent || 'unknown user agent'}</div>
                  </td>
                  <td class="px-4 py-3.5 font-mono text-sm text-text-secondary">{formatDuration(listener.duration_seconds)}</td>
                  <td class="px-4 py-3.5 text-right">
                    <button
                      onClick={() => { void disconnectListener(store, listener.id) }}
                      class="text-danger hover:text-danger/80 font-mono text-[10px] tracking-[1px] uppercase"
                    >
                      Disconnect
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {store.showMoveModal.value && (
        <div class="fixed inset-0 bg-black/60 z-50 flex items-center justify-center p-4">
          <div class="bg-surface-base border border-border rounded-xl p-5 w-full max-w-md shadow-2xl">
            <h2 class="text-lg font-bold text-text-primary mb-2">Move listeners</h2>
            <p class="text-sm text-text-tertiary mb-4">Move {selectedIds.length} selected HTTP listener(s). WebRTC clients must reconnect.</p>
            <select
              value={store.bulkTargetMount.value}
              onChange={(e) => { store.bulkTargetMount.value = (e.target as HTMLSelectElement).value }}
              class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none mb-4"
            >
              {mounts.map((mount) => <option key={mount} value={mount}>{mount}</option>)}
            </select>
            <div class="flex justify-end gap-3">
              <button onClick={() => { store.showMoveModal.value = false }} class="px-4 py-2 rounded-lg border border-border text-text-secondary">Cancel</button>
              <button onClick={() => { void moveSelectedListeners(store, selectedIds) }} class="px-4 py-2 rounded-lg bg-accent text-surface-base font-bold">Move</button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
