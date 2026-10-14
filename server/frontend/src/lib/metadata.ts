import type { MetadataEvent } from '../types'

export interface NowPlaying {
  title: string
  artist: string
  revision: number | null
  versioned: boolean
}

export function applyMetadata(current: NowPlaying, event: MetadataEvent): NowPlaying {
  if (current.revision !== null && event.revision < current.revision) return current
  return { title: event.title, artist: event.artist, revision: event.revision, versioned: true }
}

export function applyStreamTitle(current: NowPlaying, event: { title: string; artist: string }): NowPlaying {
  if (current.versioned) return current
  return { ...current, title: event.title, artist: event.artist }
}
