import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// Where the two factories of this run listen. They take a free port rather than a fixed one, because
// a machine works several issues at once and every worktree runs the same gate; the ports are found
// in the global setup and written here, because the tests read them from processes of their own.

// Beside the package, not under test-results: that directory is Playwright's own and is emptied when
// a run starts.
const file = fileURLToPath(new URL('../.factories.json', import.meta.url))

export function remember(factories) {
  writeFileSync(file, JSON.stringify(factories))
}

const factories = () => JSON.parse(readFileSync(file, 'utf8'))

// The factory working the canned queue, and the one that is paused.
export const working = (path = '') => factories().working + path
export const paused = (path = '') => factories().paused + path
// The configuration file a factory reads, which a test rewrites to pause it while it runs.
export const configuration = (name) => factories().configurations[name]
