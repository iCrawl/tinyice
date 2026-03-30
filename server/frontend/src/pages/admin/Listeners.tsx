import type { AdminData, ListenerInfo } from '../../types'
import { signal } from '@preact/signals'
import { useEffect, useRef } from 'preact/hooks'
import { api } from '../../lib/api'

type ListenersStore = ReturnType<typeof createListenersStore>

const adminData = (window.__TINYICE__ ?? {}) as Partial<AdminData>

function createListenersStore() {
  const listeners = signal<ListenerInfo[]>([])
  const loading = signal(true)
  const filterMount = signal('')
  const selectedIds = signal<Record<string, boolean>>({})
  const showMoveModal = signal(false)
  const bulkTargetMount = signal('')

  return { listeners, loading, filterMount, selectedIds, showMoveModal, bulkTargetMount }
}

function useListenersStore() {
  const storeRef = useRef<ListenersStore | null>(null)
  if (storeRef.current == null) {
    storeRef.current = createListenersStore()
  }
  return storeRef.current
}

function reconcileSelection(store: ListenersStore, nextListeners: ListenerInfo[]) {
  const knownIds = new Set(nextListeners.map((listener) => listener.id))
  const next: Record<string, boolean> = {}
  for (const [id, selected] of Object.entries(store.selectedIds.value)) {
    if (selected && knownIds.has(id)) {
      next[id] = true
    }
  }
  store.selectedIds.value = next
}

async function load(store: ListenersStore) {
  store.loading.value = true
  try {
    store.listeners.value = await api.get<ListenerInfo[]>('/api/listeners')
    reconcileSelection(store, store.listeners.value)
  } catch { /* empty */ }
  store.loading.value = false
}

async function disconnectListener(store: ListenersStore, id: string) {
  await api.post('/api/listeners/disconnect', { id })
  await load(store)
}

async function moveSelectedListeners(store: ListenersStore, ids: string[]) {
  const targetMount = store.bulkTargetMount.value
  try {
    for (const id of ids) {
      await api.post('/api/listeners/move', {
        id,
        target_mount: targetMount,
      })
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
    if (listener.current_mount && !seen.has(listener.current_mount)) {
      seen.add(listener.current_mount)
      mounts.push(listener.current_mount)
    }
    if (listener.requested_mount && !seen.has(listener.requested_mount)) {
      seen.add(listener.requested_mount)
      mounts.push(listener.requested_mount)
    }
  }
  return mounts
}

function countForMount(store: ListenersStore, mount: string): number {
  return store.listeners.value.filter((listener) => listener.current_mount === mount).length
}

function filteredListeners(store: ListenersStore): ListenerInfo[] {
  if (!store.filterMount.value) {
    return store.listeners.value
  }
  return store.listeners.value.filter((listener) => listener.current_mount === store.filterMount.value)
}

function selectedListenerIds(store: ListenersStore): string[] {
  return Object.entries(store.selectedIds.value)
    .filter(([, selected]) => selected)
    .map(([id]) => id)
}

function selectedVisibleIds(store: ListenersStore, rows: ListenerInfo[]): string[] {
  return rows
    .filter((listener) => store.selectedIds.value[listener.id])
    .map((listener) => listener.id)
}

function toggleVisibleSelection(store: ListenersStore, rows: ListenerInfo[], checked: boolean) {
  const next = { ...store.selectedIds.value }
  for (const listener of rows) {
    if (checked) {
      next[listener.id] = true
    } else {
      delete next[listener.id]
    }
  }
  store.selectedIds.value = next
}

function openMoveModal(store: ListenersStore, mounts: string[]) {
  const defaultTarget = store.filterMount.value && mounts.includes(store.filterMount.value)
    ? store.filterMount.value
    : mounts[0] || ''
  store.bulkTargetMount.value = defaultTarget
  store.showMoveModal.value = true
}

function formatDuration(totalSeconds: number): string {
  if (totalSeconds < 60) return `${totalSeconds}s`
  if (totalSeconds < 3600) return `${Math.floor(totalSeconds / 60)}m`
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  return `${hours}h ${minutes}m`
}

function mountFilterLabel(store: ListenersStore, mount: string): string {
  const count = mount ? countForMount(store, mount) : store.listeners.value.length
  return mount ? `${mount} (${count})` : `All (${count})`
}

function totalLabel(count: number): string {
  return `${count} ${count === 1 ? 'listener' : 'listeners'}`
}

export function Listeners() {
  const store = useListenersStore()
  const { loading, filterMount, selectedIds, showMoveModal, bulkTargetMount } = store

  useEffect(() => {
    void load(store)
  }, [])

  const mounts = availableMounts(store)
  const rows = filteredListeners(store)
  const visibleSelectedIds = selectedVisibleIds(store, rows)
  const allVisibleSelected = rows.length > 0 && visibleSelectedIds.length === rows.length
  const selectedCount = selectedListenerIds(store).length

  return (
    <div class="p-7">
      <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">PLAYBACK</div>
      <div class="flex items-start justify-between mb-6 gap-4">
        <div>
          <div class="flex items-center gap-3">
            <h1 class="text-xl font-bold text-text-primary">Listeners</h1>
            <span class="font-mono text-[11px] tracking-[1px] uppercase text-text-tertiary">
              {filterMount.value ? `${filterMount.value} · ${totalLabel(rows.length)}` : totalLabel(store.listeners.value.length)}
            </span>
          </div>
          <p class="text-sm text-text-tertiary mt-1">Active HTTP and WebRTC playback clients.</p>
        </div>
        <select
          value={filterMount.value}
          onChange={(e) => {
            filterMount.value = (e.target as HTMLSelectElement).value
            selectedIds.value = {}
          }}
          class="min-w-52 bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
        >
          <option value="">{mountFilterLabel(store, '')}</option>
          {mounts.map((mount) => (
            <option key={mount} value={mount}>
              {mountFilterLabel(store, mount)}
            </option>
          ))}
        </select>
      </div>

      <div class="flex items-center justify-between mb-4 gap-3">
        <span class="font-mono text-[11px] tracking-[1px] uppercase text-text-tertiary">
          {selectedCount === 0 ? 'No listeners selected' : `${selectedCount} selected`}
        </span>
        <button
          onClick={() => { openMoveModal(store, mounts) }}
          disabled={selectedCount === 0 || mounts.length === 0}
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
                      toggleVisibleSelection(store, rows, (e.target as HTMLInputElement).checked)
                    }}
                    aria-label="Select all visible listeners"
                    class="h-4 w-4 rounded border-border bg-surface-overlay"
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
              {loading.value ? (
                <tr><td colSpan={7} class="px-4 py-8 text-center text-text-tertiary text-sm">Loading...</td></tr>
              ) : rows.length === 0 ? (
                <tr><td colSpan={7} class="px-4 py-8 text-center text-text-tertiary text-sm">No active listeners</td></tr>
              ) : (
                rows.map((listener) => (
                  <tr key={listener.id} class="border-b border-[rgba(255,255,255,0.03)]">
                    <td class="px-4 py-3.5">
                      <input
                        type="checkbox"
                        checked={Boolean(selectedIds.value[listener.id])}
                        onChange={(e) => {
                          const checked = (e.target as HTMLInputElement).checked
                          selectedIds.value = checked
                            ? { ...selectedIds.value, [listener.id]: true }
                            : Object.fromEntries(Object.entries(selectedIds.value).filter(([id]) => id !== listener.id))
                        }}
                        aria-label={`Select listener ${listener.id}`}
                        class="h-4 w-4 rounded border-border bg-surface-overlay"
                      />
                    </td>
                    <td class="px-4 py-3.5">
                      <span class="inline-block font-mono text-[10px] tracking-[1px] uppercase px-2 py-0.5 rounded bg-surface-overlay text-text-secondary">
                        {listener.protocol}
                      </span>
                    </td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{listener.current_mount}</td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-secondary">{listener.requested_mount}</td>
                    <td class="px-4 py-3.5 text-sm text-text-secondary">
                      <div>{listener.remote_addr}</div>
                      <div class="text-[11px] text-text-tertiary truncate max-w-[22rem]">{listener.user_agent || 'Unknown client'}</div>
                    </td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{formatDuration(listener.duration_seconds)}</td>
                    <td class="px-4 py-3.5 text-right">
                      <button
                        onClick={() => { void disconnectListener(store, listener.id) }}
                        class="border border-border text-danger font-mono text-xs px-2 py-1.5 rounded-lg hover:border-danger/30"
                      >
                        DISCONNECT
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {showMoveModal.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div class="bg-surface-overlay border border-border rounded-xl p-6 max-w-md w-full mx-4">
            <h2 class="text-lg font-bold text-text-primary mb-1">Move Selected Listeners</h2>
            <p class="text-sm text-text-secondary mb-4">
              Move {selectedCount} {selectedCount === 1 ? 'listener' : 'listeners'} to another mount.
            </p>
            <div>
              <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">TARGET MOUNT</label>
              <select
                value={bulkTargetMount.value}
                onChange={(e) => { bulkTargetMount.value = (e.target as HTMLSelectElement).value }}
                class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
              >
                {mounts.map((mount) => (
                  <option key={mount} value={mount}>
                    {mount}
                  </option>
                ))}
              </select>
            </div>
            <div class="flex justify-end gap-2 mt-6">
              <button
                onClick={() => {
                  showMoveModal.value = false
                  bulkTargetMount.value = ''
                }}
                class="border border-border text-text-secondary font-mono text-xs px-4 py-2.5 rounded-lg hover:border-border-hover"
              >
                CANCEL
              </button>
              <button
                onClick={() => { void moveSelectedListeners(store, selectedListenerIds(store)) }}
                disabled={selectedCount === 0 || !bulkTargetMount.value}
                class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg disabled:opacity-50 disabled:cursor-not-allowed"
              >
                MOVE
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
