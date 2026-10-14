import { signal } from '@preact/signals'
import { useMemo, useEffect } from 'preact/hooks'
import { api } from '../../lib/api'
import { reportError, showToast } from '../../lib/toast'

import type { AdminStream as Stream, DiagnosticHistoryEntry } from '../../types'

function createStreamsStore() {
  const streams = signal<Stream[]>([])
  const loading = signal(true)
  const loadError = signal('')
  let fetching = false
  const showModal = signal(false)
  const editingMount = signal<string | null>(null) // null = creating
  const formMount = signal('')
  const formPassword = signal('')
  const formBurst = signal(524288)
  const formEnabled = signal(true)
  const formVisible = signal(false)
  const saveError = signal('')
  const formMaxListeners = signal(0)
  const historyMount = signal('')
  const historyEntries = signal<DiagnosticHistoryEntry[]>([])
  const historyError = signal('')
  const historyLoading = signal(false)

  function statusDotClass(s: Stream): string {
    switch (s.status) {
      case 'running': return 'bg-live'
      case 'recovering': case 'degraded': return 'bg-accent'
      case 'dead': case 'error': return 'bg-danger'
      default: return 'bg-text-tertiary'
    }
  }

  function statusLabel(s: Stream): string {
    return s.status_reason || s.status || (s.source_kind || s.source_ip ? 'Source connected' : 'No source connected')
  }

  async function openHistory(mount: string) {
    historyMount.value = mount
    historyEntries.value = []
    historyError.value = ''
    historyLoading.value = true
    try {
      historyEntries.value = await api.get<DiagnosticHistoryEntry[]>(`/api/streams/diagnostics?mount=${encodeURIComponent(mount)}&limit=20`)
    } catch (e) {
      historyError.value = e instanceof Error ? e.message : 'Could not load diagnostics'
    } finally {
      historyLoading.value = false
    }
  }

  function resetForm() {
    formMount.value = ''
    formPassword.value = ''
    formMaxListeners.value = 0
    formBurst.value = 524288
    formEnabled.value = true
    formVisible.value = false
    editingMount.value = null
    saveError.value = ''
  }

  function openAddModal() {
    resetForm()
    showModal.value = true
  }

  function openEditModal(s: Stream) {
    resetForm()
    editingMount.value = s.mount
    formMount.value = s.mount
    formBurst.value = s.burst_size || 0
    formMaxListeners.value = s.max_listeners || 0
    formEnabled.value = s.enabled
    formVisible.value = s.visible
    showModal.value = true
  }

  async function load() {
    if (fetching) return
    fetching = true
    try {
      streams.value = await api.get<Stream[]>('/api/streams')
      loadError.value = ''
    } catch (e) {
      loadError.value = e instanceof Error ? e.message : 'Failed to load streams'
    }
    loading.value = false
    fetching = false
  }

  async function saveMount() {
    saveError.value = ''
    try {
      if (editingMount.value) {
        const body: Record<string, unknown> = {
          mount: editingMount.value,
          burst_size: formBurst.value,
          max_listeners: formMaxListeners.value,
          enabled: formEnabled.value,
          visible: formVisible.value,
        }
        if (formPassword.value) {
          body.password = formPassword.value
        }
        await api.put('/api/streams', body)
      } else {
        await api.post('/api/streams', {
          mount: formMount.value,
          password: formPassword.value,
          burst_size: formBurst.value,
          max_listeners: formMaxListeners.value,
          enabled: formEnabled.value,
          visible: formVisible.value,
        })
      }
      showModal.value = false
      resetForm()
      load()
    } catch (e) {
      saveError.value = (e instanceof Error ? e.message : '') || 'Save failed'
    }
  }

  async function removeMount(mount: string) {
    if (!window.confirm(`Remove mount ${mount}?`)) return
    try {
      await api.del(`/api/streams?mount=${encodeURIComponent(mount)}`)
      showToast('success', `Mount ${mount} removed`)
    } catch (e) {
      reportError(e, `Failed to remove ${mount}`)
    }
    load()
  }

  async function kickSource(mount: string) {
    try {
      await api.post('/api/streams/kick', { mount, type: 'source' })
    } catch (e) {
      reportError(e, `Failed to kick source on ${mount}`)
    }
    load()
  }

  async function kickListeners(mount: string) {
    try {
      await api.post('/api/streams/kick', { mount, type: 'listeners' })
    } catch (e) {
      reportError(e, `Failed to kick listeners on ${mount}`)
    }
    load()
  }

  return {
    streams,
    loading,
    loadError,
    showModal,
    editingMount,
    formMount,
    formPassword,
    formBurst,
    formEnabled,
    formVisible,
    saveError,
    formMaxListeners,
    historyMount,
    historyEntries,
    historyError,
    historyLoading,
    statusDotClass,
    statusLabel,
    openHistory,
    resetForm,
    openAddModal,
    openEditModal,
    load,
    saveMount,
    removeMount,
    kickSource,
    kickListeners,
  }
}

export function Streams() {
  const {
    streams,
    loading,
    loadError,
    showModal,
    editingMount,
    formMount,
    formPassword,
    formBurst,
    formEnabled,
    formVisible,
    saveError,
    formMaxListeners,
    historyMount,
    historyEntries,
    historyError,
    historyLoading,
    statusDotClass,
    statusLabel,
    openHistory,
    resetForm,
    openAddModal,
    openEditModal,
    load,
    saveMount,
    removeMount,
    kickSource,
    kickListeners,
  } = useMemo(createStreamsStore, [])
  useEffect(() => {
    void load()
    const refresh = setInterval(() => { void load() }, 5000)
    return () => clearInterval(refresh)
  }, [])

  return (
    <div class="p-7">
      <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">MANAGE</div>
      <div class="flex items-center justify-between mb-6">
        <h1 class="text-xl font-bold text-text-primary">Streams</h1>
        <button
          onClick={openAddModal}
          class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg"
        >
          ADD MOUNT
        </button>
      </div>

      {loadError.value && <p role="alert" class="text-danger mb-4">{loadError.value}</p>}
      {/* Table */}
      <div class="border border-border rounded-xl overflow-x-auto">
        <table class="w-full">
          <thead>
            <tr class="border-b border-border">
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Status</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Mount</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Source</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Format</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Listeners</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-right px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody>
            {loading.value ? (
              <tr><td colSpan={6} class="px-4 py-8 text-center text-text-tertiary text-sm">Loading...</td></tr>
            ) : streams.value.length === 0 ? (
              <tr><td colSpan={6} class="px-4 py-8 text-center text-text-tertiary text-sm">No streams configured</td></tr>
            ) : (
              streams.value.map((s) => (
                <tr key={s.mount} class="border-b border-[rgba(255,255,255,0.03)]">
                  <td class="px-4 py-3.5">
                    <span
                      class={`inline-block w-2 h-2 rounded-full ${statusDotClass(s)}`}
                      title={statusLabel(s)}
                      aria-label={statusLabel(s)}
                    />
                    <span class="ml-2 text-xs text-text-secondary">{s.status || 'stopped'}</span>
                    <div class="text-xs text-text-tertiary mt-1">{statusLabel(s)}</div>
                    {s.last_error && <div class="text-xs text-danger mt-1">{s.last_error}</div>}
                    {s.recovery && <div class="text-xs text-text-secondary mt-1">Recovery {s.recovery.phase}, attempt {s.recovery.attempt}{s.recovery.next_retry && !s.recovery.next_retry.startsWith('0001-') ? `, retry ${new Date(s.recovery.next_retry).toLocaleString()}` : ', no retry scheduled'}{s.recovery.last_error ? `, ${s.recovery.last_error}` : ''}</div>}
                    {s.producer_last_progress && !s.producer_last_progress.startsWith('0001-') && <div class="text-xs text-text-tertiary">Producer progress {new Date(s.producer_last_progress).toLocaleString()}</div>}
                    {s.availability_last_progress && !s.availability_last_progress.startsWith('0001-') && <div class="text-xs text-text-tertiary">Output progress {new Date(s.availability_last_progress).toLocaleString()}</div>}
                  </td>
                  <td class="px-4 py-3.5 font-mono font-bold text-sm text-text-primary">
                    <div>{s.mount}</div>
                    {s.has_video && s.video_width ? (
                      <div class="font-mono text-[10px] tracking-wider text-text-tertiary font-normal mt-0.5">
                        {s.video_width}×{s.video_height}
                        {s.video_fps ? ` · ${s.video_fps.toFixed(0)} fps` : ''}
                        {s.video_kbps ? ` · ${s.video_kbps} kbps` : ''}
                        {s.video_gop ? ` · GOP ${s.video_gop.toFixed(1)}s` : ''}
                      </div>
                    ) : null}
                  </td>
                  <td class="px-4 py-3.5 text-sm text-text-secondary">{s.source_label || s.source_kind || s.source_ip || 'No source'}{s.source_ip && <div class="text-xs text-text-tertiary">{s.source_ip}</div>}</td>
                  <td class="px-4 py-3.5 text-sm text-text-secondary">{s.content_type || '—'}</td>
                  <td class="px-4 py-3.5 font-mono text-sm text-text-primary"><a href={`/admin/listeners?mount=${encodeURIComponent(s.mount)}`} class="text-accent">{s.listeners} connections</a>
                    {s.viewers !== undefined && <div class="text-xs text-text-tertiary">{s.viewers} HLS viewers</div>}
                    <div class="text-xs text-text-tertiary">Cap {s.max_listeners > 0 ? s.max_listeners : 'inherit'} configured</div>
                    <div class="text-xs text-text-tertiary">{s.effective_max_listeners > 0 ? s.effective_max_listeners : 'No'} effective ceiling</div>
                    <div class="text-xs text-text-tertiary">Burst {s.burst_size || 0} B configured</div>
                    <div class="text-xs text-text-tertiary">{s.effective_burst_size} B effective</div>
                  </td>
                  <td class="px-4 py-3.5 text-right">
                    <div class="flex items-center justify-end gap-1">
                      <button onClick={() => openHistory(s.mount)} class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg">History</button>
                      <button
                        onClick={() => openEditModal(s)}
                        title="Edit mount"
                        aria-label="Edit mount"
                        class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg hover:border-border-hover"
                      >
                        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" /><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" /></svg>
                      </button>
                      <button
                        onClick={() => kickSource(s.mount)}
                        title="Kick source"
                        aria-label="Kick source"
                        class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg hover:border-border-hover"
                      >
                        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18.36 6.64A9 9 0 005.64 18.36M5.64 5.64A9 9 0 0018.36 18.36" /><line x1="1" y1="1" x2="23" y2="23" /></svg>
                      </button>
                      <button
                        onClick={() => kickListeners(s.mount)}
                        title="Kick listeners"
                        aria-label="Kick listeners"
                        class="border border-border text-text-secondary font-mono text-xs px-2 py-1.5 rounded-lg hover:border-border-hover"
                      >
                        <svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4-4v2" /><circle cx="9" cy="7" r="4" /><line x1="18" y1="8" x2="23" y2="13" /><line x1="23" y1="8" x2="18" y2="13" /></svg>
                      </button>
                      <button
                        onClick={() => removeMount(s.mount)}
                        title="Remove mount"
                        aria-label="Remove mount"
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

      {historyMount.value && <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
        <div role="dialog" aria-modal="true" aria-label={`Diagnostics for ${historyMount.value}`} class="bg-surface-overlay border border-border rounded-xl p-6 max-w-2xl w-full mx-4 max-h-[85vh] overflow-y-auto">
          <h2 class="text-lg font-bold mb-4">Diagnostics {historyMount.value}</h2>
          {historyLoading.value ? <p>Loading...</p> : historyError.value ? <p role="alert" class="text-danger">{historyError.value}</p> : historyEntries.value.length === 0 ? <p>No recent diagnostic history.</p> : <ol class="space-y-3">{historyEntries.value.map((entry, index) => <li key={index} class="border-b border-border pb-3 text-sm">
            <div>{new Date(typeof entry.timestamp === 'number' ? entry.timestamp * 1000 : entry.timestamp).toLocaleString()} · {entry.status} · {entry.actor}</div>
            <p class="text-text-secondary">{entry.reason}</p>
            {entry.error && <p class="text-danger">{entry.error}</p>}
          </li>)}</ol>}
          <button onClick={() => { historyMount.value = '' }} class="mt-4 border border-border rounded-lg px-4 py-2">Close</button>
        </div>
      </div>}
      {/* Add / Edit Mount Modal */}
      {showModal.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/60">
          <div role="dialog" aria-modal="true" aria-label={editingMount.value ? 'Edit mount' : 'Add mount'} class="bg-surface-overlay border border-border rounded-xl p-6 max-w-md w-full mx-4">
            <h2 class="text-lg font-bold text-text-primary mb-4">
              {editingMount.value ? `Edit Mount ${editingMount.value}` : 'Add Mount'}
            </h2>
            <div class="flex flex-col gap-3">
              {!editingMount.value && (
                <div>
                  <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">MOUNT PATH</label>
                  <input
                    type="text"
                    aria-label="Mount path"
                    value={formMount.value}
                    onInput={(e) => { formMount.value = (e.target as HTMLInputElement).value }}
                    placeholder="/stream"
                    class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                  />
                </div>
              )}
              <div>
                <label class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">
                  {editingMount.value ? 'NEW SOURCE PASSWORD (LEAVE BLANK TO KEEP)' : 'SOURCE PASSWORD'}
                </label>
                <input
                  type="password"
                  aria-label="Source password"
                  value={formPassword.value}
                  onInput={(e) => { formPassword.value = (e.target as HTMLInputElement).value }}
                  class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                />
              </div>
              <div>
                  <label for="mount-burst" class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1 block">BURST SIZE IN BYTES (0 USES DEFAULT)</label>
                  <input
                    type="number"
                    id="mount-burst"
                    min={0}
                    max={8388608}
                    value={formBurst.value}
                    onInput={(e) => { formBurst.value = parseInt((e.target as HTMLInputElement).value) || 0 }}
                    class="w-full bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
                  />
              </div>
              <label class="text-xs text-text-secondary">Maximum listeners, 0 uses the server ceiling
                <input type="number" min={0} value={formMaxListeners.value} onInput={(e) => { formMaxListeners.value = Number(e.currentTarget.value) || 0 }} class="mt-1 w-full border border-border bg-surface-base rounded-lg p-2" />
              </label>
              <>
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={formEnabled.value}
                      onChange={(e) => { formEnabled.value = (e.target as HTMLInputElement).checked }}
                      class="accent-accent"
                    />
                    <span class="font-mono text-xs text-text-secondary">ENABLED (accept new source connections)</span>
                  </label>
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={formVisible.value}
                      onChange={(e) => { formVisible.value = (e.target as HTMLInputElement).checked }}
                      class="accent-accent"
                    />
                    <span class="font-mono text-xs text-text-secondary">VISIBLE (show in public listing)</span>
                  </label>
              </>
              {saveError.value && (
                <div class="text-danger font-mono text-xs">{saveError.value}</div>
              )}
            </div>
            <div class="flex justify-end gap-2 mt-6">
              <button
                onClick={() => { showModal.value = false; resetForm() }}
                class="border border-border text-text-secondary font-mono text-xs px-4 py-2.5 rounded-lg hover:border-border-hover"
              >
                CANCEL
              </button>
              <button
                onClick={saveMount}
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
