import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { setImmediate } from 'node:timers/promises'
import ts from 'typescript'

async function compileModule(url) {
  const source = await readFile(url, 'utf8')
  const compiled = ts.transpileModule(source, {
    fileName: url.pathname,
    compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.ESNext, jsx: ts.JsxEmit.ReactJSX, jsxImportSource: 'preact' },
  }).outputText
  const tree = ts.createSourceFile(url.pathname, compiled, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS)
  const imports = new Map()
  for (const node of tree.statements) {
    if (!ts.isImportDeclaration(node)) continue
    const specifier = node.moduleSpecifier.text
    const resolved = specifier.startsWith('.')
      ? await compileModule(new URL(`${specifier}.ts`, url))
      : import.meta.resolve(specifier)
    imports.set(specifier, resolved)
  }
  const transformed = ts.transform(tree, [(context) => (node) => {
    function visit(child) {
      if (ts.isImportDeclaration(child)) {
        return context.factory.updateImportDeclaration(child, child.modifiers, child.importClause,
          context.factory.createStringLiteral(imports.get(child.moduleSpecifier.text)), child.attributes)
      }
      return ts.visitEachChild(child, visit, context)
    }
    return ts.visitNode(node, visit)
  }])
  const text = ts.createPrinter().printFile(transformed.transformed[0])
  transformed.dispose()
  return `data:text/javascript;base64,${Buffer.from(text).toString('base64')}`
}

const { createStreamsStore } = await import(await compileModule(new URL('../pages/admin/Streams.tsx', import.meta.url)))

function historyRequests(t) {
  const requests = []
  t.mock.method(globalThis, 'fetch', (url) => new Promise((resolve, reject) => {
    requests.push({
      url,
      succeed(entries) { resolve(new Response(JSON.stringify(entries))) },
      fail(message) { reject(new Error(message)) },
    })
  }))
  return requests
}

const aEntry = { status: 'dead', class: 'producer', reason: 'A source failed', timestamp: '2026-10-14T12:00:00Z', actor: 'health' }
const bEntry = { status: 'recovering', class: 'producer', reason: 'B source retrying', timestamp: '2026-10-14T12:01:00Z', actor: 'health' }

test('an older mount success cannot overwrite the current dialog history', async (t) => {
  const requests = historyRequests(t)
  const store = createStreamsStore()
  const a = store.openHistory('/a')
  store.closeHistory()
  const b = store.openHistory('/b')
  assert.equal(requests[1].url, '/api/streams/diagnostics?mount=%2Fb&limit=20')
  requests[1].succeed([bEntry])
  await b
  requests[0].succeed([aEntry])
  await a
  assert.equal(store.historyMount.value, '/b')
  assert.deepEqual(store.historyEntries.value, [bEntry])
  assert.equal(store.historyError.value, '')
  assert.equal(store.historyLoading.value, false)
})

test('an older failure cannot alter the current entries, error, or loading indicator', async (t) => {
  const requests = historyRequests(t)
  const store = createStreamsStore()
  const a = store.openHistory('/a')
  const b = store.openHistory('/b')
  requests[0].fail('A request failed')
  await a
  assert.equal(store.historyMount.value, '/b')
  assert.deepEqual(store.historyEntries.value, [])
  assert.equal(store.historyError.value, '')
  assert.equal(store.historyLoading.value, true)
  requests[1].succeed([bEntry])
  await b
  assert.deepEqual(store.historyEntries.value, [bEntry])
  assert.equal(store.historyError.value, '')
  assert.equal(store.historyLoading.value, false)
})

test('closing invalidates success and finally before reopening the same mount', async (t) => {
  const requests = historyRequests(t)
  const store = createStreamsStore()
  const first = store.openHistory('/a')
  store.closeHistory()
  requests[0].succeed([aEntry])
  await first
  assert.equal(store.historyMount.value, '')
  assert.deepEqual(store.historyEntries.value, [])
  assert.equal(store.historyLoading.value, true)
  const second = store.openHistory('/a')
  requests[1].succeed([bEntry])
  await second
  assert.equal(store.historyMount.value, '/a')
  assert.deepEqual(store.historyEntries.value, [bEntry])
  assert.equal(store.historyLoading.value, false)
})

test('closing and reopening the same mount rejects the prior success after the new failure', async (t) => {
  const requests = historyRequests(t)
  const store = createStreamsStore()
  const first = store.openHistory('/a')
  store.closeHistory()
  const second = store.openHistory('/a')
  requests[1].fail('Current request failed')
  await second
  requests[0].succeed([aEntry])
  await first
  assert.equal(store.historyMount.value, '/a')
  assert.deepEqual(store.historyEntries.value, [])
  assert.equal(store.historyError.value, 'Current request failed')
  assert.equal(store.historyLoading.value, false)
})

test('closing and reopening the same mount rejects the prior error after the new success', async (t) => {
  const requests = historyRequests(t)
  const store = createStreamsStore()
  const first = store.openHistory('/a')
  store.closeHistory()
  const second = store.openHistory('/a')
  requests[1].succeed([bEntry])
  await second
  requests[0].fail('Obsolete request failed')
  await first
  assert.equal(store.historyMount.value, '/a')
  assert.deepEqual(store.historyEntries.value, [bEntry])
  assert.equal(store.historyError.value, '')
  assert.equal(store.historyLoading.value, false)
})

test('Add Mount submits selected admission and visibility flags and reloads the returned row', async (t) => {
  const created = {
    mount: '/new', source_ip: '', content_type: '', listeners: 0,
    burst_size: 262144, effective_burst_size: 262144, max_listeners: 8, effective_max_listeners: 8,
    enabled: false, visible: true, health: 0, bitrate: '', current_song: '', name: '',
    status: 'idle', status_class: '', status_reason: '',
  }
  const requests = []
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    requests.push({ url, method: options.method, body: options.body })
    return options.method === 'POST'
      ? new Response(null, { status: 204 })
      : new Response(JSON.stringify([created]))
  })
  const store = createStreamsStore()
  store.openAddModal()
  store.formMount.value = '/new'
  store.formPassword.value = 'source-secret'
  store.formBurst.value = 262144
  store.formMaxListeners.value = 8
  store.formEnabled.value = false
  store.formVisible.value = true
  await store.saveMount()
  await setImmediate()
  assert.deepEqual(requests.map(({ url, method }) => ({ url, method })), [
    { url: '/api/streams', method: 'POST' }, { url: '/api/streams', method: 'GET' },
  ])
  assert.deepEqual(JSON.parse(requests[0].body), {
    mount: '/new', password: 'source-secret', burst_size: 262144,
    max_listeners: 8, enabled: false, visible: true,
  })
  assert.equal(store.showModal.value, false)
  assert.equal(store.saveError.value, '')
  assert.deepEqual(store.streams.value, [created])
})
