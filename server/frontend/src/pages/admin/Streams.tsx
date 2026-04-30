import type { DiagnosticHistoryEntry } from '../../types'
import { signal } from '@preact/signals'
import { useEffect, useRef } from 'preact/hooks'
import { api } from '../../lib/api'

interface Stream {
  mount: string
  source_ip: string
  source_kind?: string
  source_label?: string
  content_type: string
  listeners: number
  burst_size: number
  effective_burst_size: number
  max_listeners: number
  enabled: boolean
  visible: boolean
  health: number
  bitrate: string
  current_song: string
  name: string
  status: string
  status_class: string
  status_reason: string
  last_error?: string
  history: DiagnosticHistoryEntry[]
}

type StreamsStore = ReturnType<typeof createStreamsStore>

function createStreamsStore() {
  const streams = signal<Stream[]>([])
  const loading = signal(true)
  const showModal = signal(false)
  const showHistoryModal = signal(false)
  const historyMount = signal('')
  const historyEntries = signal<DiagnosticHistoryEntry[]>([])
  const editingMount = signal<Stream | null>(null)
  const editBurst = signal(0)
  const editMaxListeners = signal(0)
  const formMount = signal('')
  const formPassword = signal('')
  const formBurst = signal(65536)
  const formMaxListeners = signal(0)

  return {
    streams,
    loading,
    showModal,
    showHistoryModal,
    historyMount,
    historyEntries,
    editingMount,
    editBurst,
    editMaxListeners,
    formMount,
    formPassword,
    formBurst,
    formMaxListeners,
  }
}

function useStreamsStore() {
  const storeRef = useRef<StreamsStore | null>(null)
  if (storeRef.current == null) {
    storeRef.current = createStreamsStore()
  }
  return storeRef.current
}

async function load(store: StreamsStore) {
  store.loading.value = true
  try {
    store.streams.value = await api.get<Stream[]>('/api/streams')
  } catch { /* empty */ }
  store.loading.value = false
}

async function addMount(store: StreamsStore) {
  await api.post('/api/streams', {
    mount: store.formMount.value,
    password: store.formPassword.value,
    burst_size: store.formBurst.value,
    max_listeners: store.formMaxListeners.value,
  })
  store.showModal.value = false
  store.formMount.value = ''
  store.formPassword.value = ''
  store.formBurst.value = 65536
  store.formMaxListeners.value = 0
  await load(store)
}

function openEditModal(store: StreamsStore, stream: Stream) {
  store.editingMount.value = stream
  store.editBurst.value = stream.burst_size || 0
  store.editMaxListeners.value = stream.max_listeners || 0
}

function closeEditModal(store: StreamsStore) {
  store.editingMount.value = null
  store.editBurst.value = 0
  store.editMaxListeners.value = 0
}

async function saveEditModal(store: StreamsStore) {
  const mount = store.editingMount.value?.mount
  if (!mount) return

  await api.put('/api/streams', {
    mount,
    burst_size: store.editBurst.value || 0,
    max_listeners: store.editMaxListeners.value || 0,
  })
  closeEditModal(store)
  await load(store)
}

async function removeMount(store: StreamsStore, mount: string) {
  await api.del(`/api/streams?mount=${encodeURIComponent(mount)}`)
  await load(store)
}

async function kickSource(store: StreamsStore, mount: string) {
  await api.post('/api/streams/kick', { mount, type: 'source' })
  await load(store)
}

async function kickListeners(store: StreamsStore, mount: string) {
  await api.post('/api/streams/kick', { mount, type: 'listeners' })
  await load(store)
}

function hasConnectedSource(stream: Stream) {
  return Boolean(stream.source_ip || stream.source_kind)
}

function statusLabel(stream: Stream) {
  const status = stream.status
  switch (status) {
    case 'running': return 'Running'
    case 'recovering': return 'Recovering'
    case 'degraded': return 'Degraded'
    case 'dead': return 'Dead'
    case 'error': return 'Error'
    case 'stopped': return 'Stopped'
    default: return hasConnectedSource(stream) ? 'Running' : 'Stopped'
  }
}

function statusBadgeClass(stream: Stream) {
  const effective = stream.status || (hasConnectedSource(stream) ? 'running' : 'stopped')
  switch (effective) {
    case 'running':
      return 'bg-live/15 text-live'
    case 'recovering':
      return 'bg-accent/15 text-accent'
    case 'degraded':
      return 'bg-yellow-500/15 text-yellow-400'
    case 'dead':
    case 'error':
      return 'bg-danger/15 text-danger'
    default:
      return 'bg-surface-overlay text-text-tertiary'
  }
}

function sourceStatusReason(stream: Stream) {
  if (stream.status_reason) return stream.status_reason
  if (stream.source_label) return `${stream.source_label} connected`
  return hasConnectedSource(stream) ? 'source connected' : 'no source'
}

function sourceDisplay(stream: Stream) {
  return stream.source_ip || stream.source_label || 'No source'
}

function latestHistoryEntry(stream: Stream) {
  return stream.history?.[0] ?? null
}

function burstSizeDisplay(stream: Stream) {
  if (stream.burst_size > 0) {
    return `${stream.burst_size}`
  }
  return `${stream.effective_burst_size} default`
}

function formatHistoryTimestamp(timestamp: string | number) {
  const value = typeof timestamp === 'number' ? timestamp * 1000 : timestamp
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'Unknown time'
  return date.toLocaleString()
}

function openHistoryModal(store: StreamsStore, stream: Stream) {
  store.historyMount.value = stream.mount
  store.historyEntries.value = stream.history ?? []
  store.showHistoryModal.value = true
}

function closeHistoryModal(store: StreamsStore) {
  store.showHistoryModal.value = false
  store.historyMount.value = ''
  store.historyEntries.value = []
}

export function Streams() {
  const store = useStreamsStore()
  const { streams, loading, showModal, showHistoryModal, historyMount, historyEntries, editingMount, editBurst, editMaxListeners, formMount, formPassword, formBurst, formMaxListeners } = store

  useEffect(() => {
    void load(store)
  }, [])

  return (
    <div class="p-7">
      <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">MANAGE</div>
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-xl font-bold text-text-primary">Streams</h1>
        <button
          onClick={() => { showModal.value = true }}
          class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg"
        >
          ADD MOUNT
        </button>
      </div>

      <div class="admin-table-shell">
        <div class="admin-table-scroll">
          <table class="w-full min-w-[980px]">
            <thead>
              <tr class="border-b border-border">
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Status</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Mount</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Source IP</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Format</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Listeners</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Burst</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Cap</th>
                <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-right px-4 py-3">Actions</th>
              </tr>
            </thead>
            <tbody>
              {loading.value ? (
                <tr><td colSpan={8} class="px-4 py-8 text-center text-text-tertiary text-sm">Loading...</td></tr>
              ) : streams.value.length === 0 ? (
                <tr><td colSpan={8} class="px-4 py-8 text-center text-text-tertiary text-sm">No streams configured</td></tr>
              ) : (
                streams.value.map((s) => (
                  <tr key={s.mount} class="border-b border-[rgba(255,255,255,0.03)]">
                    <td class="px-4 py-3.5">
                      {(() => {
                        const latestEvent = latestHistoryEntry(s)
                        return (
                      <div class="flex flex-col gap-1.5">
                        <span class={`inline-flex self-start items-center rounded-full px-2 py-1 font-mono text-[10px] uppercase ${statusBadgeClass(s)}`}>
                          {statusLabel(s)}
                        </span>
                        <span class="text-xs text-text-secondary">
                          {sourceStatusReason(s)}
                        </span>
                        {latestEvent && (
                          <div class="flex items-center gap-2 text-[11px] text-text-tertiary">
                            <span>Recent: {latestEvent.reason}</span>
                            {latestEvent.error && <span class="truncate text-danger">{latestEvent.error}</span>}
                            {s.history.length > 1 && (
                              <button
                                onClick={() => { openHistoryModal(store, s) }}
                                class="font-mono uppercase tracking-[1px] text-accent hover:text-accent/80"
                              >
                                History
                              </button>
                            )}
                          </div>
                        )}
                      </div>
                        )
                      })()}
                    </td>
                    <td class="px-4 py-3.5 font-mono font-bold text-sm text-text-primary">{s.mount}</td>
                    <td class="px-4 py-3.5 text-sm text-text-secondary">{sourceDisplay(s)}</td>
                    <td class="px-4 py-3.5 text-sm text-text-secondary">{s.content_type || '—'}</td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{s.listeners}</td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{burstSizeDisplay(s)}</td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{s.max_listeners}</td>
                    <td class="px-4 py-3.5 text-right">
                      <div class="flex items-center justify-end gap-1">
                        <button
                          onClick={() => { openEditModal(store, s) }}
                          title={`Edit settings for ${s.mount}`}
                          class="border border-border text-accent font-mono text-xs px-2 py-1.5 rounded-lg hover:border-accent/40"
                        >
                          EDIT
                        </button>
                        <button
                          onClick={() => { void kickSource(store, s.mount) }}
                          aria-label={`Kick source on ${s.mount}`}
                          title="Kick source"
                          class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg hover:border-border-hover"
                        >
                          <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18.36 6.64A9 9 0 005.64 18.36M5.64 5.64A9 9 0 0018.36 18.36" /><line x1="1" y1="1" x2="23" y2="23" /></svg>
                        </button>
                        <button
                          onClick={() => { void kickListeners(store, s.mount) }}
                          aria-label={`Kick listeners on ${s.mount}`}
                          title="Kick listeners"
                          class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg hover:border-border-hover"
                        >
                          <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4-4v2" /><circle cx="9" cy="7" r="4" /><line x1="18" y1="8" x2="23" y2="13" /><line x1="23" y1="8" x2="18" y2="13" /></svg>
                        </button>
                        <button
                          onClick={() => { void removeMount(store, s.mount) }}
                          aria-label={`Remove mount ${s.mount}`}
                          title="Remove mount"
                          class="border border-border text-danger font-mono text-xs px-2 py-1.5 rounded-lg hover:border-danger/30"
                        >
                          <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6" /><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" /></svg>
                        </button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {showHistoryModal.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div class="bg-surface-overlay border border-border rounded-xl p-6 max-w-2xl w-full mx-4">
            <h2 class="text-lg font-bold text-text-primary mb-1">History for {historyMount.value}</h2>
            <p class="text-sm text-text-secondary mb-4">Most recent diagnostic events for this mount.</p>
            <div class="max-h-[60vh] overflow-y-auto border border-border rounded-lg">
              {historyEntries.value.length === 0 ? (
                <div class="px-4 py-8 text-center text-text-tertiary text-sm">No recent diagnostic events</div>
              ) : (
                <div class="divide-y divide-border">
                  {historyEntries.value.map((entry, index) => (
                    <div key={`${entry.timestamp}-${index}`} class="px-4 py-3">
                      <div class="flex items-center justify-between gap-4">
                        <span class="font-mono text-[10px] uppercase tracking-[1px] text-text-tertiary">
                          {entry.status}
                        </span>
                        <span class="font-mono text-[10px] text-text-tertiary">
                          {formatHistoryTimestamp(entry.timestamp)}
                        </span>
                      </div>
                      <div class="text-sm text-text-primary mt-1">{entry.reason}</div>
                      {entry.error && (
                        <div class="text-xs text-danger mt-1 break-all">{entry.error}</div>
                      )}
                      {entry.details?.path && (
                        <div class="text-xs text-text-secondary mt-1 break-all">
                          Path: <span class="font-mono text-text-primary">{entry.details.path}</span>
                        </div>
                      )}
                      <div class="text-[11px] text-text-tertiary mt-1">
                        {entry.actor}{entry.class ? ` • ${entry.class}` : ''}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
            <div class="flex justify-end gap-2 mt-6">
              <button
                onClick={() => { closeHistoryModal(store) }}
                class="border border-border text-text-secondary font-mono text-xs px-4 py-2.5 rounded-lg hover:border-border-hover"
              >
                CLOSE
              </button>
            </div>
          </div>
        </div>
      )}

      {showModal.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div class="bg-surface-overlay border border-border rounded-xl p-6 max-w-md w-full mx-4">
            <h2 class="text-lg font-bold text-text-primary mb-4">Add Mount</h2>
            <div class="flex flex-col gap-3">
              <div>
                <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">MOUNT PATH</label>
                <input
                  type="text"
                  value={formMount.value}
                  onInput={(e) => { formMount.value = (e.target as HTMLInputElement).value }}
                  placeholder="/stream"
                  class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                />
              </div>
              <div>
                <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">SOURCE PASSWORD</label>
                <input
                  type="password"
                  value={formPassword.value}
                  onInput={(e) => { formPassword.value = (e.target as HTMLInputElement).value }}
                  class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                />
              </div>
              <div>
                <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">BURST SIZE</label>
                <input
                  type="number"
                  value={formBurst.value}
                  onInput={(e) => { formBurst.value = parseInt((e.target as HTMLInputElement).value) || 0 }}
                  class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                />
              </div>
              <div>
                <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">MAX LISTENERS</label>
                <input
                  type="number"
                  value={formMaxListeners.value}
                  onInput={(e) => { formMaxListeners.value = parseInt((e.target as HTMLInputElement).value) || 0 }}
                  class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                />
              </div>
            </div>
            <div class="flex justify-end gap-2 mt-6">
              <button
                onClick={() => { showModal.value = false }}
                class="border border-border text-text-secondary font-mono text-xs px-4 py-2.5 rounded-lg hover:border-border-hover"
              >
                CANCEL
              </button>
              <button
                onClick={() => { void addMount(store) }}
                class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg"
              >
                SAVE
              </button>
            </div>
          </div>
        </div>
      )}

      {editingMount.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div class="bg-surface-overlay border border-border rounded-xl p-6 max-w-md w-full mx-4">
            <h2 class="text-lg font-bold text-text-primary mb-1">Edit Mount Settings</h2>
            <p class="font-mono text-xs text-text-tertiary mb-4">{editingMount.value.mount}</p>
            <div class="flex flex-col gap-3">
              <div>
                <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">BURST SIZE</label>
                <input
                  type="number"
                  value={editBurst.value}
                  onInput={(e) => { editBurst.value = parseInt((e.target as HTMLInputElement).value) || 0 }}
                  class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                />
              </div>
              <div>
                <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">MAX LISTENERS</label>
                <input
                  type="number"
                  value={editMaxListeners.value}
                  onInput={(e) => { editMaxListeners.value = parseInt((e.target as HTMLInputElement).value) || 0 }}
                  class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                />
              </div>
            </div>
            <div class="flex justify-end gap-2 mt-6">
              <button
                onClick={() => { closeEditModal(store) }}
                class="border border-border text-text-secondary font-mono text-xs px-4 py-2.5 rounded-lg hover:border-border-hover"
              >
                CANCEL
              </button>
              <button
                onClick={() => { void saveEditModal(store) }}
                class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg"
              >
                SAVE
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
