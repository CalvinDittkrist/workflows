import { execFileSync, spawn } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { remember } from './where.js'

// The browser test watches the real thing: this builds the factory binary with the dashboard it
// embeds and starts it twice in fake mode. The first works its canned queue down to the state the
// tests read — five runs done and the last entry, whose scripted worker hangs, still running, which
// holds until the deadline. The second is paused, so the whole queue stays in its order.

const factoryDir = fileURLToPath(new URL('../..', import.meta.url))

const CANNED_DONE = 5
const CANNED_QUEUE = 6
const READY = 60_000

export default async function start() {
  const dir = mkdtempSync(join(tmpdir(), 'factory-ui-'))
  const binary = join(dir, 'factory')
  execFileSync('go', ['build', '-o', binary, '.'], { cwd: factoryDir, stdio: 'inherit' })

  const stops = []
  const stopAll = () => stops.forEach((stop) => stop())
  process.on('exit', stopAll)
  try {
    const ports = await freePorts(['working', 'paused'])
    const working = run(binary, dir, 'working', ports.working, [])
    const paused = run(binary, dir, 'paused', ports.paused, ['-paused'])
    stops.push(working.stop, paused.stop)
    await Promise.all([
      state(ports.working, 'its canned queue to be worked', (line) => line.done.length === CANNED_DONE && line.now.length === 1),
      state(ports.paused, 'its queue to be derived', (line) => line.queue.length === CANNED_QUEUE && line.now.length === 0),
    ])
    remember({ working: url(ports.working), paused: url(ports.paused) })
    return async () => {
      stopAll()
      await Promise.all([working.ended, paused.ended])
      rmSync(dir, { recursive: true, force: true })
    }
  } catch (error) {
    stopAll()
    throw error
  }
}

const url = (on) => `http://127.0.0.1:${on}`

// freePorts asks the operating system for one port per name, the way the factory's own Go tests do.
// All of them are held at the same time, so it cannot hand the same one out twice, and they are let
// go together, right before the factories bind them.
async function freePorts(names) {
  const held = await Promise.all(names.map(hold))
  const ports = Object.fromEntries(names.map((name, i) => [name, held[i].address().port]))
  await Promise.all(held.map((server) => new Promise((closed) => server.close(closed))))
  return ports
}

// hold takes a port nobody holds and keeps it until it is closed.
function hold() {
  return new Promise((held, failed) => {
    const server = createServer()
    server.once('error', failed)
    server.listen(0, '127.0.0.1', () => held(server))
  })
}

// run starts one factory in fake mode on its own port and data directory.
function run(binary, dir, name, on, flags) {
  const config = join(dir, `${name}.json`)
  writeFileSync(
    config,
    JSON.stringify({
      listen: `127.0.0.1:${on}`,
      deadline: '10m', // the hanging run has to still be running when the last test reads the page
      poll: '200ms',
      data_dir: join(dir, name),
      repositories: ['acme/edge-sensors', 'acme/backtest'],
    }),
  )
  const factory = spawn(binary, ['-config', config, '-fake', ...flags], { stdio: 'inherit' })
  return {
    // SIGTERM is how the factory ends the worker it is running; killing it outright would leave the
    // scripted worker's process group on the machine.
    stop: () => {
      if (factory.exitCode === null) factory.kill('SIGTERM')
    },
    ended: new Promise((done) => factory.once('exit', done)),
  }
}

// state waits until a factory's line is what the tests read.
async function state(on, what, reached) {
  const line = `${url(on)}/api/line`
  const until = Date.now() + READY
  let last = 'no answer yet'
  while (Date.now() < until) {
    try {
      const now = await fetch(line).then((r) => r.json())
      if (reached(now)) return
      last = `${now.done.length} done, ${now.now.length} running, ${now.queue.length} queued`
    } catch (error) {
      last = error.message
    }
    await new Promise((done) => setTimeout(done, 200))
  }
  throw new Error(`the factory on ${line} did not reach ${what} within ${READY / 1000}s (${last})`)
}
