import type { MetadataEvent, StreamEvent } from '../types'

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

export function applyStreamTitle(current: NowPlaying, event: Pick<StreamEvent, 'title' | 'artist' | 'revision'>): NowPlaying {
  if (event.revision !== undefined) {
    if (current.revision !== null && event.revision < current.revision) return current
    return { title: event.title, artist: event.artist, revision: event.revision, versioned: true }
  }
  if (current.versioned) return current
  return { ...current, title: event.title, artist: event.artist }
}

export function createMetadataCallbacks({ mount, initial, update }: {
  mount: string | undefined
  initial: Pick<NowPlaying, 'title' | 'artist'>
  update: (value: NowPlaying) => void
}) {
  let current: NowPlaying = { ...initial, revision: null, versioned: false }
  return {
    onOpen() {
      current = { ...current, revision: null }
    },
    metadata(event: MetadataEvent) {
      if (event.mount !== mount) return
      current = applyMetadata(current, event)
      update(current)
    },
    stream(event: StreamEvent) {
      if (event.mount !== mount) return
      current = applyStreamTitle(current, event)
      update(current)
    },
  }
}
