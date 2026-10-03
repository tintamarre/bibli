// Bibli — screenshots of every screen, from the code in the working tree.
//
//     node scripts/screenshots.mjs [-o DIR] [--only NAME,NAME] [--keep]
//
// Builds the binary, creates a throwaway database, loads the demonstration
// dataset, starts a server on a free port, drives a headless Chrome through
// the screens and writes one PNG per screen. Everything it creates outside
// the output directory is deleted on the way out.
//
// Why a script rather than a person with a screenshot key: the demonstration
// dataset is dated relative to the day it is loaded (app/demo.sql), so a
// picture of it ages — "5 jours de retard" is only true on the day it was
// taken. A run reproduces the whole set in a minute, which is what makes the
// images in a README or an article cheap to keep in step with the interface.
//
// No dependency is installed. It needs `go`, `sqlite3` (as the README already
// does), and a Chrome or Chromium already on the machine — including the one
// Playwright caches, which is why the search below looks there. Node ≥ 22 for
// the global WebSocket: the browser is driven over the Chrome DevTools
// Protocol, and a CDP client is a few dozen lines of it.
//
// Two shots reach the Internet, and say so below: the cataloguing screen asks
// the BnF, and covers are fetched by the server. Offline they still render,
// with an empty record and no cover.

import { spawn, spawnSync } from 'node:child_process'
import { createServer } from 'node:net'
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const password = 'demo'

// The library name and the tracking token are presentation, not data: demo.sql
// leaves both unset, since a bare instance starts that way. A screenshot wants
// them filled — an empty header and a missing tracking page show nothing.
const library = 'Bibliothèque de Lincé'
const token = '7f3c1a9b4e2d8c6f0a5b3e7d1c9f4a2b'
const tokenBorrower = 'LEC10035' // Adam N., P3 — two books out, none late

// Codes are literals in demo.sql, so they are the same on every run.
const CARD = 'LEC10035'
const BOOKS = ['VOL913042', 'VOL442747'] // Le Loup qui voulait…, Devine combien…
const RETURNED = 'VOL292568' // Jean de la Lune, out to Tom (P3)
const NEW_ISBN = '9782070584628' // Harry Potter — not in the dataset, known to the BnF
const LABELS = ['VOL030662', 'VOL989982', 'VOL486970', 'VOL442747',
  'VOL229651', 'VOL321361', 'VOL913042', 'VOL811045'].join(',')

// `fit` crops the capture to the content instead of to the viewport: a short
// screen otherwise ends in half a page of empty background. It is a JavaScript
// expression evaluated in the page, returning a height in CSS pixels.
const belowMain = 'document.querySelector("main").getBoundingClientRect().bottom + 24'
// A list is cut at a row boundary, never through one: which index lands on the
// last row differs per screen, which is why each one names its own.
const belowRow = (n) =>
  `(() => { const r = document.querySelectorAll("table tr");
    return r[Math.min(${n}, r.length - 1)].getBoundingClientRect().bottom + 16 })()`
const belowEverything =
  `(() => { const b = [...document.querySelectorAll("*")]
    .map((e) => e.getBoundingClientRect().bottom).filter((y) => y < 4000)
    return Math.max(...b) + 32 })()`

// A scripted screen waits on HTMX, which answers over the network: the sleeps
// are what stands in for a person watching the row appear.
const wait = 'const s = (ms) => new Promise((r) => setTimeout(r, ms))'

// Order matters: a shot that writes to the database comes after every shot
// that reads what it would change. Only "return" writes — the basket of
// "borrow" lives in the browser until confirmed, and "catalogue" stops on the
// filled record without saving it.
const SHOTS = [
  { name: 'home', url: '/', fit: belowMain },

  { name: 'loans', url: '/loans', height: 1200, fit: belowRow(8), wait: 800 },

  {
    name: 'borrow',
    url: '/borrow',
    height: 1000,
    fit: 'document.getElementById("loan").getBoundingClientRect().bottom + 40',
    script: `(async () => { ${wait}
      const card = document.querySelector('form[hx-post="/borrow/borrower"]')
      card.querySelector('input.scan').value = ${JSON.stringify(CARD)}
      htmx.trigger(card, 'submit')
      await s(1500)
      for (const code of ${JSON.stringify(BOOKS)}) {
        const form = document.getElementById('form-book')
        form.querySelector('#scan-book').value = code
        htmx.trigger(form, 'submit')
        await s(1500)
      }
      document.activeElement.blur() })()`
  },

  {
    // Reaches the BnF, then Open Library and the BnF again for the cover.
    name: 'catalogue',
    url: '/catalogue',
    height: 1200,
    fit: belowMain,
    wait: 1000,
    script: `(async () => { ${wait}
      const form = document.querySelector('#catalogue form')
      form.querySelector('input[name=isbn]').value = ${JSON.stringify(NEW_ISBN)}
      form.requestSubmit()
      await s(10000)
      document.activeElement.blur()
      window.scrollTo(0, 0) })()`
  },

  { name: 'book', url: '/book/29', height: 1200, fit: belowRow(5), wait: 2000 },
  { name: 'borrower', url: '/borrowers/121', height: 1200, fit: belowRow(7), wait: 800 },
  { name: 'inventory', url: '/inventory', height: 1200, fit: belowRow(6), wait: 2000 },
  { name: 'labels', url: `/print/labels?codes=${LABELS}`, width: 1100, fit: belowEverything, wait: 1500 },
  { name: 'cards', url: '/borrowers/cards?group=P4', width: 1100, height: 880, wait: 1500 },
  { name: 'stats', url: '/stats', height: 1400, fit: belowMain },
  { name: 'settings', url: '/settings', height: 1200, fit: belowMain },

  // The tracking page is read on a phone, so it is taken on one.
  { name: 'tracking', url: `/track/${token}`, width: 430, height: 900, mobile: true, fit: belowMain },

  {
    name: 'return',
    url: '/return',
    fit: belowMain,
    wait: 800,
    script: `(async () => { ${wait}
      const form = document.querySelector('form[hx-post="/return"]')
      form.querySelector('input.scan').value = ${JSON.stringify(RETURNED)}
      htmx.trigger(form, 'submit')
      await s(2000)
      document.activeElement.blur() })()`
  }
]

// --- command line ---------------------------------------------------------

const argv = process.argv.slice(2)
const flag = (name, fallback) => {
  const i = argv.indexOf(name)
  return i >= 0 && argv[i + 1] ? argv[i + 1] : fallback
}
const out = path.resolve(flag('-o', flag('--out', path.join(root, 'screenshots'))))
const only = flag('--only', '').split(',').filter(Boolean)
const keep = argv.includes('--keep')
const shots = only.length ? SHOTS.filter((s) => only.includes(s.name)) : SHOTS

if (!shots.length) {
  console.error(`no shot named ${only.join(', ')} — known: ${SHOTS.map((s) => s.name).join(', ')}`)
  process.exit(1)
}

// --- the browser ----------------------------------------------------------

// In order of preference: what the caller named, what Playwright cached (the
// likeliest one on a developer machine that has ever run a browser test), then
// the usual installed paths.
const chromeCandidates = [
  process.env.CHROME,
  ...(process.platform === 'darwin'
    ? [
        `${process.env.HOME}/Library/Caches/ms-playwright/chromium-*/chrome-mac*/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing`,
        `${process.env.HOME}/Library/Caches/ms-playwright/chromium-*/chrome-mac*/Chromium.app/Contents/MacOS/Chromium`,
        '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
        '/Applications/Chromium.app/Contents/MacOS/Chromium'
      ]
    : [
        `${process.env.HOME}/.cache/ms-playwright/chromium-*/chrome-linux/chrome`,
        '/usr/bin/google-chrome',
        '/usr/bin/chromium',
        '/usr/bin/chromium-browser'
      ])
].filter(Boolean)

// A glob is resolved by hand rather than by a shell: the cached revision is a
// number that changes with every Playwright release, and the newest one wins.
const resolveChrome = () => {
  for (const candidate of chromeCandidates) {
    if (!candidate.includes('*')) {
      if (existsSync(candidate)) return candidate
      continue
    }
    const [prefix, ...rest] = candidate.split('*')
    const dir = path.dirname(prefix)
    if (!existsSync(dir)) continue
    const base = path.basename(prefix)
    const matches = spawnSync('ls', [dir], { encoding: 'utf8' }).stdout
      .split('\n').filter((name) => name.startsWith(base)).sort().reverse()
    for (const match of matches) {
      const full = path.join(dir, match) + rest.join('')
      if (existsSync(full)) return full
    }
  }
  return null
}

// --- helpers --------------------------------------------------------------

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const run = (cmd, args, opts = {}) => {
  const result = spawnSync(cmd, args, { stdio: 'inherit', cwd: root, ...opts })
  if (result.status !== 0) throw new Error(`${cmd} ${args.join(' ')} failed`)
  return result
}

const freePort = () => new Promise((resolve, reject) => {
  const probe = createServer()
  probe.on('error', reject)
  probe.listen(0, '127.0.0.1', () => {
    const { port } = probe.address()
    probe.close(() => resolve(port))
  })
})

const waitForServer = async (base) => {
  for (let i = 0; i < 80; i++) {
    try {
      const response = await fetch(`${base}/healthcheck`)
      if (response.ok) return
    } catch { /* not listening yet */ }
    await sleep(250)
  }
  throw new Error('the server never answered /healthcheck')
}

// --- build, database, server ----------------------------------------------

const work = path.join(tmpdir(), `bibli-shots-${process.pid}`)
mkdirSync(work, { recursive: true })
mkdirSync(out, { recursive: true })

const bin = path.join(work, 'bibli')
const db = path.join(work, 'demo.db')
const cache = path.join(work, 'cache')

let server = null
let chrome = null

// A child told to stop is not stopped yet: Chrome goes on writing its profile
// into the work directory for a moment, and the server holds the database
// open. Deleting the directory or loading the database under them fails, or
// half-succeeds, so each is waited for — killed outright if it lingers.
const exited = (child) => new Promise((resolve) => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return resolve()
  child.once('exit', resolve)
})

const halt = async (child) => {
  if (!child) return
  child.kill()
  const timer = setTimeout(() => child.kill('SIGKILL'), 5000)
  await exited(child)
  clearTimeout(timer)
}

const removeWork = () => {
  if (!keep) rmSync(work, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 })
}

const stop = async () => {
  await Promise.all([halt(server), halt(chrome)])
  server = chrome = null
  removeWork()
}

// The last resort, on an exit nobody awaited (an exception, process.exit):
// synchronous, so it can only ask the children to go and try its best.
process.on('exit', () => {
  for (const child of [server, chrome]) {
    if (child && child.exitCode === null) child.kill('SIGKILL')
  }
  try { removeWork() } catch { /* a leftover in the temp directory, not a failure */ }
})
process.on('SIGINT', async () => { await stop(); process.exit(130) })

const startServer = async (port) => {
  const child = spawn(bin, [
    '-db', db, '-addr', `127.0.0.1:${port}`,
    '-secure-cookies=false', '-backup-dir', '', '-cache-dir', cache
  ], { env: { ...process.env, BIBLI_ADMIN_PASSWORD: password }, stdio: 'ignore' })
  await waitForServer(`http://127.0.0.1:${port}`)
  return child
}

console.log('building…')
run('go', ['build', '-o', bin, './app'])

// The schema is the binary's business, not this script's: it is created by a
// first start, which is also what proves the build runs at all. demo.sql is
// then loaded into a database that already has its tables.
const port = await freePort()
console.log('creating the database…')
server = await startServer(port)
await halt(server)

run('sqlite3', [db], {
  input: readFileSync(path.join(root, 'app/demo.sql'), 'utf8'),
  stdio: ['pipe', 'inherit', 'inherit']
})
run('sqlite3', [db,
  `UPDATE setting SET value = '${library}' WHERE key = 'library_name';` +
  `UPDATE borrower SET tracking_token = '${token}' WHERE card_code = '${tokenBorrower}';`])

const base = `http://127.0.0.1:${port}`
server = await startServer(port)

// --- the CDP session ------------------------------------------------------

const executable = resolveChrome()
if (!executable) {
  console.error('no Chrome or Chromium found. Set CHROME to one:\n  ' +
    chromeCandidates.join('\n  '))
  process.exit(1)
}

const debugPort = await freePort()
chrome = spawn(executable, [
  '--headless=new', `--remote-debugging-port=${debugPort}`,
  `--user-data-dir=${path.join(work, 'chrome')}`,
  '--hide-scrollbars', '--no-first-run', '--no-default-browser-check',
  '--disable-extensions', '--force-color-profile=srgb',
  '--window-size=1400,1000', 'about:blank'
], { stdio: 'ignore' })

let version = null
for (let i = 0; i < 60 && !version; i++) {
  try { version = await (await fetch(`http://127.0.0.1:${debugPort}/json/version`)).json() } catch { await sleep(250) }
}
if (!version) throw new Error('Chrome never opened its debugging port')

const ws = new WebSocket(version.webSocketDebuggerUrl)
await new Promise((resolve, reject) => { ws.onopen = resolve; ws.onerror = reject })

let nextId = 0
const pending = new Map()
const events = []
ws.onmessage = (message) => {
  const msg = JSON.parse(message.data)
  if (msg.id === undefined) { events.push(msg); return }
  const waiter = pending.get(msg.id)
  pending.delete(msg.id)
  msg.error ? waiter.reject(new Error(JSON.stringify(msg.error))) : waiter.resolve(msg.result)
}

const send = (method, params = {}, sessionId) => new Promise((resolve, reject) => {
  const id = ++nextId
  pending.set(id, { resolve, reject })
  ws.send(JSON.stringify({ id, method, params, sessionId }))
})

// One tab for the whole run: the session cookie set by the login then rides
// along, which is what saves logging in on every screen.
const { targetId } = await send('Target.createTarget', { url: 'about:blank' })
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true })
const cmd = (method, params) => send(method, params, sessionId)

await cmd('Page.enable')
await cmd('Runtime.enable')

const waitForEvent = async (name, timeout = 20000) => {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    const i = events.findIndex((e) => e.method === name && e.sessionId === sessionId)
    if (i >= 0) return events.splice(i, 1)[0]
    await sleep(50)
  }
  throw new Error(`timed out waiting for ${name}`)
}

const evaluate = async (expression) => {
  const result = await cmd('Runtime.evaluate', {
    expression, awaitPromise: true, returnByValue: true
  })
  if (result.exceptionDetails) {
    throw new Error(`${result.exceptionDetails.text} ${JSON.stringify(result.result)}`)
  }
  return result.result.value
}

const go = async (url) => {
  events.length = 0
  await cmd('Page.navigate', { url })
  await waitForEvent('Page.loadEventFired')
  await sleep(400)
}

// --- log in, then capture -------------------------------------------------

await go(`${base}/login`)
events.length = 0
const submitted = await evaluate(`(() => {
  const form = document.querySelector('form[action="/login"]')
  if (!form) return false
  form.querySelector('input[name=password]').value = ${JSON.stringify(password)}
  form.submit()
  return true })()`)
if (submitted) await waitForEvent('Page.loadEventFired')
await sleep(500)

for (const shot of shots) {
  const width = shot.width || 1280
  const height = shot.height || 860
  const metrics = { width, deviceScaleFactor: 2, mobile: !!shot.mobile }

  await cmd('Emulation.setDeviceMetricsOverride', { ...metrics, height })
  await go(base + shot.url)
  if (shot.script) await evaluate(shot.script)
  await sleep(shot.wait || 300)

  // Measured, then taken: the viewport is resized to the height the content
  // turned out to need, so the picture has no dead space and no second pass
  // through an image editor.
  if (shot.fit) {
    const fitted = Math.ceil(await evaluate(shot.fit))
    await cmd('Emulation.setDeviceMetricsOverride', { ...metrics, height: fitted })
    await sleep(250)
  }

  const { data } = await cmd('Page.captureScreenshot', { format: 'png' })
  const file = path.join(out, `${shot.name}.png`)
  writeFileSync(file, Buffer.from(data, 'base64'))
  console.log(`  ${path.relative(process.cwd(), file)}`)
}

ws.close()
await stop()
console.log(`\n${shots.length} screenshots in ${out}`)
