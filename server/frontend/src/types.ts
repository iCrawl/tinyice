// Data injected by Go server into window.__TINYICE__
export interface TinyIceBase {
  csrfToken: string
  version: string
  pageTitle: string
  pageSubtitle: string
  branding: {
    logoUrl: string | null
    accentColor: string
    landingMarkdown: string
  }
}

export interface PlayerData extends TinyIceBase {
  mount: string
  title: string
  artist: string
  format: 'mp3' | 'opus'
  bitrate: number
  listeners: number
  hasWebRTC: boolean
}

export interface AdminData extends TinyIceBase {
  user: { username: string; role: 'superadmin' | 'admin' }
  mounts: string[]
}

export interface LandingData extends TinyIceBase {
  streams: StreamInfo[]
}

export interface StreamInfo {
  mount: string
  title: string
  artist: string
  format: string
  bitrate: number
  listeners: number
  burst_size: number
  max_listeners: number
  live: boolean
  status: string
  status_reason: string
  history: DiagnosticHistoryEntry[]
}

export interface ListenerInfo {
  id: string
  protocol: 'http' | 'webrtc'
  requested_mount: string
  current_mount: string
  remote_addr: string
  user_agent: string
  connected_at: number
  duration_seconds: number
  last_stream_switch_at: number
}

export interface DiagnosticHistoryEntry {
  timestamp: string | number
  status: string
  class: string
  reason: string
  error?: string
  actor: string
  details?: Record<string, string>
}

// SSE Events
export interface StatsEvent {
  listeners: number
  streams: number
  bandwidth: number
  bandwidth_in: number
  bandwidth_out: number
  bytes_in: number
  bytes_out: number
  uptime: number
  goroutines: number
  memory: number
  gc: number
}

export interface StreamEvent {
  mount: string
  title: string
  artist: string
  format: string
  bitrate: number
  listeners: number
  health: number
  is_transcoded?: boolean
  // For transcoded outputs: the source mount + its format/bitrate
  // so the dashboard can render "<src-format> → <out-format>".
  source_mount?: string
  source_type?: string
  source_bitrate?: string
  video_width?: number
  video_height?: number
  video_fps?: number
  video_gop?: number
  video_kbps?: number
  status: string
  status_reason: string
}

export interface AutoDJEvent {
  mount: string
  state: 'playing' | 'paused' | 'stopped'
  currentTrack: { title: string; artist: string; file: string }
  position: number
  duration: number
  queue: string[]
}

// API types
export interface PlaylistItem {
  id: string
  file: string
  title: string
  artist: string
  duration: number
}

export interface FileInfo {
  name: string
  path: string
  isDir: boolean
  title?: string
  artist?: string
  duration?: number
  bitrate?: number
}

declare global {
  interface Window {
    __TINYICE__: TinyIceBase | PlayerData | AdminData | LandingData
  }
}
