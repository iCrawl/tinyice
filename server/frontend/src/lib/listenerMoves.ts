import type { ListenerInfo, ListenerMove } from '../types'

export function reconcilePendingMoves(pending: Record<string, ListenerMove>, listeners: ListenerInfo[]): Record<string, ListenerMove> {
  return Object.fromEntries(Object.entries(pending).filter(([id, move]) => {
    const listener = listeners.find((item) => item.id === id)
    return listener && listener.move_id !== move.id
  }))
}

export function describeListenerMove(listener: ListenerInfo, queued?: ListenerMove): string {
  if (queued) return `Pending move to ${queued.destination}`
  switch (listener.move_status) {
    case 'pending': return listener.move_destination ? `Pending move to ${listener.move_destination}` : 'Move pending'
    case 'completed': return 'Move completed'
    case 'failed': return `Move failed${listener.move_error ? `, ${listener.move_error}` : ''}`
    default: return ''
  }
}
