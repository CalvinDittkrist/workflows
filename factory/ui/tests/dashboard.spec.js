import { expect, test } from '@playwright/test'
import { paused, working } from './where.js'

// The dashboard read the way the maintainer reads it: in a browser, against the real binary in fake
// mode. The canned queue is worked before the tests start (tests/factory.js), so the runs below are
// the scripted ones: 1 ready, 2 blocked, 3 failed, 4 failed, 5 ready with a warning, 6 still running.

const READY_RUN = 1
const BLOCKED_RUN = 2
const WARNED_RUN = 5
const RUNNING_RUN = 6

const detail = (page) => page.locator('.detail')

test('the three areas render from the canned data', async ({ page }) => {
  await page.goto(working('/'))

  const repositories = page.locator('.repos li')
  await expect(repositories).toHaveCount(2)
  await expect(repositories.first()).toContainText('acme/edge-sensors')
  await expect(repositories.last()).toContainText('acme/backtest')

  const now = page.locator('.row.now')
  await expect(now).toContainText('#118')
  await expect(now).toContainText('Document the calibration procedure')
  await expect(now).toContainText('acme/backtest')

  await expect(page.locator('.line')).toContainText('Queue0')
  await expect(page.locator('.line .none')).toHaveText('empty')

  const done = page.locator('.line button.row:not(.now)')
  await expect(done).toHaveCount(5)
  // Newest first, each with how it ended.
  await expect(done.first()).toContainText('#121')
  await expect(done.first()).toContainText('ready')
  await expect(done.last()).toContainText('#104')
  await expect(done.last()).toContainText('ready')
  await expect(done.nth(3)).toContainText('blocked')
})

test('the whole queue is shown in its order while the factory is paused', async ({ page }) => {
  await page.goto(paused('/'))

  await expect(page.locator('.mode')).toHaveText('paused')
  // What waits per repository, which is the only number on that line.
  await expect(page.locator('.repos li').first().locator('b')).toHaveText('4')
  await expect(page.locator('.repos li').last().locator('b')).toHaveText('2')

  const queue = page.locator('.line ol .row')
  await expect(queue).toHaveCount(6)
  // Oldest routing label first, whichever repository it is in.
  const order = ['#104', '#109', '#112', '#115', '#121', '#118']
  for (const [position, issue] of order.entries()) {
    await expect(queue.nth(position)).toContainText(`${position + 1}`)
    await expect(queue.nth(position)).toContainText(issue)
  }
  await expect(page.locator('.line .none').first()).toHaveText('paused')
})

test('a run is selected through the URL and the selection survives a reload', async ({ page }) => {
  await page.goto(working(`/#run=${BLOCKED_RUN}`))
  await expect(detail(page).locator('h3')).toContainText('#109')

  await page.reload()
  await expect(detail(page).locator('h3')).toContainText('#109')

  // Selecting in the line puts the run in the URL, so the page can be linked as it is read.
  await page.locator('.line button.row', { hasText: '#104' }).click()
  await expect(page).toHaveURL(new RegExp(`#run=${READY_RUN}$`))
  await expect(detail(page).locator('h3')).toContainText('#104')
})

test('the stage line and the outcome box show the scripted states', async ({ page }) => {
  await page.goto(working(`/#run=${READY_RUN}`))
  const stages = detail(page).locator('.steps li')
  await expect(stages).toHaveText(['implement', 'review', 'pr', 'ci', 'reviews'])
  // The ready run went through every stage and stopped in the last one it invoked.
  await expect(stages.nth(0)).toHaveClass('done')
  await expect(stages.nth(4)).toHaveClass(/at/)
  await expect(detail(page).locator('.outcome')).toContainText('ready')
  await expect(detail(page).getByRole('link')).toHaveAttribute(
    'href',
    'https://github.com/acme/edge-sensors/pull/204',
  )

  await page.goto(working(`/#run=${BLOCKED_RUN}`))
  // The blocked run stopped in the review stage and never reached the ones after it.
  await expect(detail(page).locator('.steps .at')).toHaveText('review')
  await expect(detail(page).locator('.steps li').nth(2)).toHaveClass('')
  await expect(detail(page).locator('.outcome')).toContainText('blocked')
  await expect(detail(page).locator('.outcome')).toContainText('supersede ADR 0012')

  await page.goto(working(`/#run=${RUNNING_RUN}`))
  // The run that is still going has no outcome box at all.
  await expect(detail(page).locator('.state')).toHaveText('running')
  await expect(detail(page).locator('.steps .at')).toHaveText('implement')
  await expect(detail(page).locator('.outcome')).toHaveCount(0)
})

test('the selected run shows what it cost, how full its context came and what it warned about', async ({
  page,
}) => {
  await page.goto(working(`/#run=${WARNED_RUN}`))
  await expect(detail(page).locator('.facts').first()).toContainText('$4.18')
  await expect(detail(page).locator('.facts').first()).toContainText('23 turns')
  await expect(detail(page).locator('.facts').first()).toContainText(/\d+\.\dk context peak/)
  await expect(detail(page).locator('.warnings li')).toContainText('left a process behind')
})

test('the live log sets the events of the worker’s subagents in', async ({ page }) => {
  await page.goto(working(`/#run=${READY_RUN}`))
  const log = detail(page).locator('.log')
  await expect(log.locator('.ev-result')).toContainText('result: success')
  const subagent = log.locator('.ev-sub').first()
  await expect(subagent).toContainText('Read the diff under review')
  // Set in: what a subagent did stands further right than what the worker itself did.
  await expect(subagent.locator('.what')).toHaveCSS('padding-left', '18px')
  await expect(log.locator('.ev:not(.ev-sub) .what').first()).toHaveCSS('padding-left', '0px')

  // An event with a body opens it where it stands; one without cannot be opened.
  const call = log.locator('.ev-tool').first()
  await expect(call.locator('pre')).toHaveCount(0)
  await call.getByRole('button').click()
  await expect(call.locator('pre')).toContainText('plugins/worker/skills/work/SKILL.md')
})

test('the log of a running worker grows as it is written, and repeats nothing', async ({ page }) => {
  await page.goto(working(`/#run=${RUNNING_RUN}`))
  const lines = detail(page).locator('.log .ev')
  await expect(lines.first()).toBeVisible()
  const [started, first] = [await lines.count(), await lines.first().textContent()]

  // The scripted worker of this run says something new every second, and the page asks only for what
  // it has not seen yet: the log has to grow by those lines and keep the ones already read.
  await expect.poll(() => lines.count(), { timeout: 15_000 }).toBeGreaterThan(started)
  expect(await lines.first().textContent()).toBe(first)
  const read = await lines.allTextContents()
  expect(new Set(read).size).toBe(read.length) // an event that was read twice would stand twice
})

test('the factory says when it waits for quota and until when', async ({ page }) => {
  // The factory serves this state from the ticket that adds the quota check on; the dashboard reads
  // it from the interface it already promises, so the answer is the one under test here.
  await page.route('**/api/status', async (route) => {
    const answer = await route.fetch()
    const status = await answer.json()
    await route.fulfill({
      json: { ...status, state: 'waiting-for-quota', quotaUntil: '2026-09-21T16:45:00Z' },
    })
  })
  await page.goto(working('/'))
  await expect(page.locator('.mode')).toContainText('waiting for quota')
  await expect(page.locator('.mode')).toContainText('until')
})

test('a factory that stops answering is said so, and a run it does not have too', async ({ page }) => {
  // A run reached by editing the URL that this factory never ran.
  await page.goto(working('/#run=999'))
  await expect(detail(page).locator('.none')).toHaveText('run 999 is not on this factory')

  // The run that is still being followed, while the factory answers nothing but errors.
  await page.goto(working(`/#run=${RUNNING_RUN}`))
  await expect(detail(page).locator('h3')).toContainText('#118')
  await page.route('**/api/**', (route) => route.fulfill({ status: 500, body: 'no' }))

  // The banner stands under the header, where it is read, and the run says what stopped answering.
  await expect(page.locator('.banner')).toBeVisible()
  const [header, said, repositories] = await Promise.all(
    ['.top', '.banner', '.repos'].map((part) => page.locator(part).boundingBox()),
  )
  expect(said.y).toBe(header.y + header.height)
  expect(repositories.y).toBe(said.y + said.height)
  await expect(detail(page).locator('.trouble')).toContainText(`/api/runs/${RUNNING_RUN}`)
})

test('the dashboard sends no writing request', async ({ page }) => {
  const written = []
  page.on('request', (request) => {
    if (!['GET', 'HEAD'].includes(request.method())) written.push(`${request.method()} ${request.url()}`)
  })

  await page.goto(working('/'))
  // The run that is still going, because it is the one the page keeps asking about.
  await page.locator('.line button.row', { hasText: '#118' }).click()
  await expect(detail(page).locator('h3')).toContainText('#118')
  await detail(page).locator('.ev-tool').first().getByRole('button').click()
  // Two rounds of every poll the page makes, so a request it only sends later would be seen here.
  await Promise.all([twice(page, '/api/line'), twice(page, '/api/runs/'), twice(page, '/api/status')])

  expect(written).toEqual([])
})

const twice = (page, endpoint) => {
  const answered = (response) => response.url().includes(endpoint)
  return page.waitForResponse(answered).then(() => page.waitForResponse(answered))
}

test('the layout holds', async ({ page }) => {
  await page.goto(working(`/#run=${READY_RUN}`))
  await expect(detail(page).locator('.ev-result')).toBeVisible()

  // The three areas stand next to each other, each in its place, whatever the content is.
  const [repos, line, run] = await Promise.all(
    ['.repos', '.line', '.detail'].map((pane) => page.locator(pane).boundingBox()),
  )
  expect(repos.x).toBe(0)
  expect(repos.width).toBe(240)
  expect(line.x).toBe(240)
  expect(line.width).toBe(460)
  expect(run.x).toBe(700)
  expect(run.width).toBe(740)
  expect(repos.height).toBe(line.height)
  expect(line.height).toBe(run.height)

  // Everything that differs between two readings of the same state is a tick: the durations that
  // count up and the clock times in the log. The rest is compared to the screenshot approved for
  // this operating system, once the fonts it is written in have arrived.
  await page.evaluate(() => document.fonts.ready)

  // The tolerance leaves room for a machine that rasterises the same glyphs a little differently,
  // and for nothing more: one changed number on the page moves 291 pixels, measured.
  await expect(page).toHaveScreenshot('dashboard.png', {
    mask: [page.locator('.tick')],
    maskColor: '#101010', // the background, so the approved look can be read off the baseline
    animations: 'disabled',
    caret: 'hide',
    maxDiffPixels: 100,
  })
})
