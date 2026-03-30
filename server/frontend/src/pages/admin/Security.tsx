import { signal } from '@preact/signals'
import { useEffect, useRef } from 'preact/hooks'
import { api } from '../../lib/api'

interface AuditEntry {
  id: number
  timestamp: string
  username: string
  action: string
  resource_type: string
  resource_id: string
  detail: string
  ip: string
}

type SecurityStore = ReturnType<typeof createSecurityStore>

function createSecurityStore() {
  const bans = signal<string[]>([])
  const whitelist = signal<string[]>([])
  const loading = signal(true)
  const banInput = signal('')
  const whitelistInput = signal('')
  const securityTab = signal<'rules' | 'audit'>('rules')
  const auditEntries = signal<AuditEntry[]>([])
  const auditTotal = signal(0)
  const auditPage = signal(1)
  const auditCategory = signal('')
  const auditLoading = signal(false)

  return {
    bans,
    whitelist,
    loading,
    banInput,
    whitelistInput,
    securityTab,
    auditEntries,
    auditTotal,
    auditPage,
    auditCategory,
    auditLoading,
  }
}

function useSecurityStore() {
  const storeRef = useRef<SecurityStore | null>(null)
  if (storeRef.current == null) {
    storeRef.current = createSecurityStore()
  }
  return storeRef.current
}

function timeAgo(dateStr: string): string {
  const now = Date.now()
  const then = new Date(dateStr).getTime()
  const diff = Math.max(0, now - then)
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} min ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} hour${hours > 1 ? 's' : ''} ago`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} day${days > 1 ? 's' : ''} ago`
  const d = new Date(dateStr)
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
}

function actionBadgeClass(action: string): string {
  const base = 'inline-flex items-center justify-center px-2 py-0.5 rounded text-[10px] font-mono font-bold tracking-wider border'
  if (/created|approved|^login$/.test(action)) return `${base} bg-green-500/15 text-green-400 border-green-500/20`
  if (/deleted|denied|revoked|login_failed/.test(action)) return `${base} bg-red-500/15 text-red-400 border-red-500/20`
  if (/updated|uploaded/.test(action)) return `${base} bg-blue-500/15 text-blue-400 border-blue-500/20`
  if (action === 'logout') return `${base} bg-surface-overlay text-text-tertiary border-border`
  return `${base} bg-surface-overlay text-text-secondary border-border`
}

const AUDIT_LIMIT = 25
const AUDIT_CATEGORIES = ['', 'auth', 'streams', 'autodj', 'relays', 'transcoders', 'users', 'security', 'settings']
const CATEGORY_LABELS: Record<string, string> = {
  '': 'All',
  auth: 'Auth',
  streams: 'Streams',
  autodj: 'AutoDJ',
  relays: 'Relays',
  transcoders: 'Transcoders',
  users: 'Users',
  security: 'Security',
  settings: 'Settings',
}

async function load(store: SecurityStore) {
  store.loading.value = true
  try {
    const [b, w] = await Promise.all([
      api.get<string[]>('/api/security/bans'),
      api.get<string[]>('/api/security/whitelist'),
    ])
    store.bans.value = b
    store.whitelist.value = w
  } catch { /* empty */ }
  store.loading.value = false
}

async function loadAudit(store: SecurityStore) {
  store.auditLoading.value = true
  try {
    const params = new URLSearchParams({
      page: String(store.auditPage.value),
      limit: String(AUDIT_LIMIT),
    })
    if (store.auditCategory.value) params.set('category', store.auditCategory.value)
    const res = await api.get<{ entries: AuditEntry[]; total: number; page: number; limit: number }>(
      `/api/security/audit?${params.toString()}`,
    )
    store.auditEntries.value = res.entries || []
    store.auditTotal.value = res.total
  } catch { /* empty */ }
  store.auditLoading.value = false
}

async function addBan(store: SecurityStore) {
  if (!store.banInput.value.trim()) return
  await api.post('/api/security/bans', { ip: store.banInput.value.trim() })
  store.banInput.value = ''
  await load(store)
}

async function removeBan(store: SecurityStore, ip: string) {
  await api.del(`/api/security/bans?ip=${encodeURIComponent(ip)}`)
  await load(store)
}

async function addWhitelist(store: SecurityStore) {
  if (!store.whitelistInput.value.trim()) return
  await api.post('/api/security/whitelist', { ip: store.whitelistInput.value.trim() })
  store.whitelistInput.value = ''
  await load(store)
}

async function removeWhitelist(store: SecurityStore, ip: string) {
  await api.del(`/api/security/whitelist?ip=${encodeURIComponent(ip)}`)
  await load(store)
}

function IpSection({
  title,
  description,
  items,
  inputValue,
  onInput,
  onAdd,
  onRemove,
  placeholder,
}: {
  title: string
  description: string
  items: string[]
  inputValue: string
  onInput: (v: string) => void
  onAdd: () => void
  onRemove: (ip: string) => void
  placeholder: string
}) {
  return (
    <div class="mb-8">
      <h2 class="text-base font-bold text-text-primary mb-1">{title}</h2>
      <p class="text-sm text-text-tertiary mb-4">{description}</p>
      <div class="flex gap-2 mb-4">
        <input
          type="text"
          value={inputValue}
          onInput={(e) => onInput((e.target as HTMLInputElement).value)}
          onKeyDown={(e) => { if (e.key === 'Enter') onAdd() }}
          placeholder={placeholder}
          class="flex-1 bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
        />
        <button
          onClick={onAdd}
          class="bg-accent text-surface-base font-mono font-bold text-xs tracking-[1px] px-4 py-2.5 rounded-lg"
        >
          ADD IP
        </button>
      </div>
      <div class="border border-border rounded-xl overflow-hidden">
        <table class="w-full">
          <thead>
            <tr class="border-b border-border">
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">IP / CIDR</th>
              <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-right px-4 py-3">Actions</th>
            </tr>
          </thead>
          <tbody>
            {items.length === 0 ? (
              <tr><td colSpan={2} class="px-4 py-6 text-center text-text-tertiary text-sm">None configured</td></tr>
            ) : (
              items.map((ip) => (
                <tr key={ip} class="border-b border-[rgba(255,255,255,0.03)]">
                  <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{ip}</td>
                  <td class="px-4 py-3.5 text-right">
                    <button
                      onClick={() => onRemove(ip)}
                      class="border border-border text-danger font-mono text-xs px-3 py-1.5 rounded-lg hover:border-danger/30"
                    >
                      REMOVE
                    </button>
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

function AuditLog({ store }: { store: SecurityStore }) {
  useEffect(() => {
    void loadAudit(store)
  }, [])

  const totalPages = Math.max(1, Math.ceil(store.auditTotal.value / AUDIT_LIMIT))
  const start = (store.auditPage.value - 1) * AUDIT_LIMIT + 1
  const end = Math.min(store.auditPage.value * AUDIT_LIMIT, store.auditTotal.value)

  return (
    <div>
      <div class="mb-4">
        <select
          value={store.auditCategory.value}
          onChange={(e) => {
            store.auditCategory.value = (e.target as HTMLSelectElement).value
            store.auditPage.value = 1
            void loadAudit(store)
          }}
          class="bg-[rgba(255,255,255,0.03)] border border-border rounded-lg px-4 py-2.5 text-text-primary font-mono text-sm focus:border-accent outline-none"
        >
          {AUDIT_CATEGORIES.map((cat) => (
            <option key={cat} value={cat}>{CATEGORY_LABELS[cat]}</option>
          ))}
        </select>
      </div>

      {store.auditLoading.value ? (
        <p class="text-text-tertiary text-sm">Loading...</p>
      ) : store.auditEntries.value.length === 0 ? (
        <div class="text-center py-12 text-text-tertiary text-sm">No audit log entries</div>
      ) : (
        <>
          <div class="border border-border rounded-xl overflow-hidden">
            <table class="w-full">
              <thead>
                <tr class="border-b border-border">
                  <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Time</th>
                  <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">User</th>
                  <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Action</th>
                  <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">Resource</th>
                  <th class="font-mono text-[9px] tracking-[1px] text-text-tertiary uppercase text-left px-4 py-3">IP</th>
                </tr>
              </thead>
              <tbody>
                {store.auditEntries.value.map((entry) => (
                  <tr key={entry.id} class="border-b border-[rgba(255,255,255,0.03)]">
                    <td class="px-4 py-3.5 text-sm text-text-secondary whitespace-nowrap" title={new Date(entry.timestamp).toLocaleString()}>
                      {timeAgo(entry.timestamp)}
                    </td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-primary">{entry.username}</td>
                    <td class="px-4 py-3.5">
                      <span class={actionBadgeClass(entry.action)}>{entry.action}</span>
                      {entry.detail && (
                        <div class="text-[10px] text-text-tertiary mt-1">{entry.detail}</div>
                      )}
                    </td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-secondary">
                      {entry.resource_type}{entry.resource_id ? `: ${entry.resource_id}` : ''}
                    </td>
                    <td class="px-4 py-3.5 font-mono text-sm text-text-tertiary">{entry.ip}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div class="flex items-center justify-between mt-4">
            <span class="text-text-tertiary font-mono text-xs">
              Showing {start}-{end} of {store.auditTotal.value}
            </span>
            <div class="flex gap-2">
              <button
                onClick={() => { store.auditPage.value = store.auditPage.value - 1; void loadAudit(store) }}
                disabled={store.auditPage.value <= 1}
                class="border border-border text-text-secondary font-mono text-xs px-3 py-1.5 rounded-lg hover:border-border-hover disabled:opacity-30"
              >
                PREV
              </button>
              <button
                onClick={() => { store.auditPage.value = store.auditPage.value + 1; void loadAudit(store) }}
                disabled={store.auditPage.value >= totalPages}
                class="border border-border text-text-secondary font-mono text-xs px-3 py-1.5 rounded-lg hover:border-border-hover disabled:opacity-30"
              >
                NEXT
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  )
}

export function Security() {
  const store = useSecurityStore()
  const { bans, whitelist, loading, banInput, whitelistInput, securityTab } = store

  useEffect(() => {
    void load(store)
  }, [])

  return (
    <div class="p-7">
      <div class="font-mono text-[10px] tracking-[2px] text-text-tertiary mb-1">MANAGE</div>
      <h1 class="text-xl font-bold text-text-primary mb-6">Security</h1>

      <div class="flex gap-1 mb-6 border-b border-border">
        {([['rules', 'BANS & WHITELIST'], ['audit', 'AUDIT LOG']] as const).map(([key, label]) => (
          <button
            key={key}
            onClick={() => { securityTab.value = key }}
            class={`font-mono text-xs tracking-[1px] uppercase px-4 py-2.5 border-b-2 transition-colors ${
              securityTab.value === key
                ? 'border-accent text-accent'
                : 'border-transparent text-text-tertiary hover:text-text-secondary'
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {securityTab.value === 'rules' ? (
        loading.value ? (
          <p class="text-text-tertiary text-sm">Loading...</p>
        ) : (
          <>
            <IpSection
              title="Banned IPs"
              description="Connections from these IPs will be rejected."
              items={bans.value}
              inputValue={banInput.value}
              onInput={(v) => { banInput.value = v }}
              onAdd={() => { void addBan(store) }}
              onRemove={(ip) => { void removeBan(store, ip) }}
              placeholder="192.168.1.0/24"
            />
            <IpSection
              title="Whitelisted IPs"
              description="Only these IPs will be allowed to connect. Leave empty to allow all."
              items={whitelist.value}
              inputValue={whitelistInput.value}
              onInput={(v) => { whitelistInput.value = v }}
              onAdd={() => { void addWhitelist(store) }}
              onRemove={(ip) => { void removeWhitelist(store, ip) }}
              placeholder="10.0.0.0/8"
            />
          </>
        )
      ) : (
        <AuditLog store={store} />
      )}
    </div>
  )
}
