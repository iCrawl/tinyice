import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const source = await readFile(new URL('./listenerMoves.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext } }).outputText
const { reconcilePendingMoves, describeListenerMove } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const move = { id: 'move-1', status: 'pending', listener_id: 'listener-1', destination: '/backup' }
const listener = { id: 'listener-1', current_mount: '/live', requested_mount: '/live', protocol: 'http' }

test('accepted moves remain pending while a snapshot still precedes the move', () => {
  assert.deepEqual(reconcilePendingMoves({ 'listener-1': move }, [listener]), { 'listener-1': move })
  assert.equal(describeListenerMove(listener, move), 'Pending move to /backup')
})

test('matching snapshots replace queued feedback with the actual pending or completed status', () => {
  const snapshot = { ...listener, move_id: 'move-1', move_status: 'pending' }
  assert.deepEqual(reconcilePendingMoves({ 'listener-1': move }, [snapshot]), {})
  assert.equal(describeListenerMove(snapshot), 'Move pending')
  assert.equal(describeListenerMove({ ...snapshot, current_mount: '/backup', move_status: 'completed' }), 'Move completed')
})

test('failed moves expose the backend failure rather than reporting success', () => {
  const failed = { ...listener, move_id: 'move-1', move_status: 'failed', move_error: 'Destination full' }
  assert.deepEqual(reconcilePendingMoves({ 'listener-1': move }, [failed]), {})
  assert.equal(describeListenerMove(failed), 'Move failed, Destination full')
})

test('disconnected listeners do not leave queued operation state behind', () => {
  assert.deepEqual(reconcilePendingMoves({ 'listener-1': move }, []), {})
})
