import { defineConfig, devices } from '@playwright/test'

// The browser test runs against the real binary in fake mode, started by tests/factory.js: the
// dashboard is read the way the maintainer reads it, over HTTP, from the embedded build.
export const port = Number(process.env.FACTORY_PORT || 7342)
// A second factory, started paused, so the whole canned queue is shown in its order and the paused
// status is read from a real factory rather than from a stubbed answer.
export const pausedPort = port + 1
export const pausedURL = `http://127.0.0.1:${pausedPort}`

export default defineConfig({
  testDir: './tests',
  // One factory with one canned queue: the tests read the same line and must not race for it.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
  globalSetup: './tests/factory.js',
  snapshotPathTemplate: '{testDir}/screenshots/{arg}{ext}',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    ...devices['Desktop Chrome'],
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    colorScheme: 'dark',
    trace: 'retain-on-failure',
  },
})
