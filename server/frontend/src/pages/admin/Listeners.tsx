import type { ListenerInfo } from '../../types'
import { signal } from '@preact/signals'
import { useEffect } from 'preact/hooks'
import { api } from '../../lib/api'

const listeners = signal<ListenerInfo[]>([])
const loading = signal(true)
const filterMount = signal('')
const moveTargets = signal<Record<string, string>>({})

function syncMoveTargets(nextListeners: ListenerInfo[]) {
  const next: Record<string, string> = {}
  for (const listener of nextListeners) {
    next[listener.id] = listener.current_mount
  }
  moveTargets.value = next
}

async function load() {
  loading.value = true
  try {
    listeners.value = await api.get<ListenerInfo[]>('/api/listeners')
    syncMoveTargets(listeners.value)
  } catch { /* empty */ }
  loading.value = false
}

async function disconnectListener(id: string) {
  await api.post('/api/listeners/disconnect', { id })
  load()
}

async function moveListener(id: string) {
  await api.post('/api/listeners/move', {
    id,
    target_mount: moveTargets.value[id] || '',
  })
  load()
}

function filteredListeners(): ListenerInfo[] {
  if (!filterMount.value) {
    return listeners.value
  }
  return listeners.value.filter((listener) =>
    listener.current_mount.includes(filterMount.value) || listener.requested_mount.includes(filterMount.value),
  )
}

function formatDuration(totalSeconds: number): string {
  if (totalSeconds < 60) return `${totalSeconds}s`
  if (totalSeconds < 3600) return `${Math.floor(totalSeconds / 60)}m`
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  return `${hours}h ${minutes}m`
}

export function Listeners() {
  useEffect(() => { load() }, [])

  const rows = filteredListeners()

  return (
    <div class="p-7">
      <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">PLAYBACK</div>
      <div class="flex items-center justify-between mb-6 gap-4">
        <div>
          <h1 class="text-xl font-bold text-text-primary">Listeners</h1>
          <p class="text-sm text-text-tertiary mt-1">Active HTTP and WebRTC playback clients.</p>
        </div>
        <input
          type="text"
          value={filterMount.value}
          onInput={(e) => { filterMount.value = (e.target as HTMLInputElement).value }}
          placeholder="Filter by mount"
          class="w-48 bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
        />
      </div>

      <div class="border border-border rounded-xl overflow-hidden">
        <table class="w-full">
          <thead>
            <tr class="border-b border-border">
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Protocol</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Current</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Requested</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Client</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Connected</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Move To</th>
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
                  <td class="px-4 py-3.5">
                    <input
                      type="text"
                      value={moveTargets.value[listener.id] || ''}
                      onInput={(e) => {
                        moveTargets.value = {
                          ...moveTargets.value,
                          [listener.id]: (e.target as HTMLInputElement).value,
                        }
                      }}
                      class="w-28 bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-3 py-2 text-text-primary font-mono text-xs focus:border-accent outline-none"
                    />
                  </td>
                  <td class="px-4 py-3.5 text-right">
                    <div class="flex items-center justify-end gap-2">
                      <button
                        onClick={() => moveListener(listener.id)}
                        class="border border-border text-accent font-mono text-xs px-2 py-1.5 rounded-lg hover:border-accent/40"
                      >
                        MOVE
                      </button>
                      <button
                        onClick={() => disconnectListener(listener.id)}
                        class="border border-border text-danger font-mono text-xs px-2 py-1.5 rounded-lg hover:border-danger/30"
                      >
                        DISCONNECT
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
  )
}
