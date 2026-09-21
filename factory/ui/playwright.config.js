import { defineConfig, devices } from '@playwright/test'

// The browser test runs against the real binary in fake mode, started by tests/factory.js: the
// dashboard is read the way the maintainer reads it, over HTTP, from the embedded build. The two
// factories take free ports and tell the tests about them through tests/where.js, so two worktrees
// can run the gate at the same time.
export default defineConfig({
  testDir: './tests',
  // One factory with one canned queue: the tests read the same line and must not race for it.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
  globalSetup: './tests/factory.js',
  // One approved screenshot per operating system: the layout is the same everywhere, the way glyphs
  // are rasterised is not, and a baseline that has to absorb that would hold nothing.
  snapshotPathTemplate: '{testDir}/screenshots/{arg}-{platform}{ext}',
  use: {
    ...devices['Desktop Chrome'],
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 1,
    colorScheme: 'dark',
    trace: 'retain-on-failure',
  },
})
