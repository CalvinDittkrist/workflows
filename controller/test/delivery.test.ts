// The delivery graph through XState's pure functions: every outcome of every node of the delivery table
// has an edge, every guard is named, and every budget ends in a park once it is spent.
import { expect, test } from 'vitest'
import { transition } from 'xstate'
import { delivery, type DeliveryContext, deliveryContext } from '../src/delivery.js'
import { graphOf } from '../src/graphs.js'
import type { StageRecord } from '../src/records.js'

// The outcomes and events of each node, as the delivery table names them.
const table: Record<string, string[]> = {
  implement: ['complete', 'input', 'blocked', 'failed', 'message'],
  gate: ['pass', 'skipped', 'fail', 'failed'],
  'gate-fix': ['complete', 'input', 'blocked', 'failed'],
  review: ['pass', 'findings', 'failed'],
  'review-fix': ['complete', 'input', 'blocked', 'failed'],
  pr: ['opened', 'found', 'finished', 'failed'],
  ci: ['green', 'merged', 'unmergeable', 'answered', 'closed', 'failed', 'checks-failed', 'conflicts', 'comments', 'follow-up'],
  'ci-fix': ['complete', 'failed'],
  'address-reviews': ['complete', 'failed'],
}

const remain: DeliveryContext = { gateFixes: 0, gateRounds: 3, reviewRound: 1, reviewRounds: 3, repairs: 0, repairRounds: 3, yolo: false, panelPassed: false }
const spent: DeliveryContext = { ...remain, gateFixes: 3, reviewRound: 3, repairs: 3 }

// step is where an event takes a node: the next node, or the park it keeps the process on.
function step(node: string, context: DeliveryContext, type: string, mandate?: 'writer' | 'bot'): string {
  const snapshot = delivery.resolveState({ value: node, context })
  const e = { type, ...(mandate ? { mandate } : {}) }
  if (!snapshot.can(e)) return 'no edge'
  const [next, actions] = transition(delivery, snapshot, e)
  const park = (actions as { type: string; params?: { state?: string } }[]).find((a) => a.type === 'park')
  return park ? `parked ${park.params?.state}` : String(next.value)
}

test('the delivery graph has the nodes of the delivery table, and only done is final', () => {
  const states = delivery.toJSON().states as Record<string, { type?: string }>
  expect(delivery.id).toBe('delivery')
  expect(Object.keys(states).sort()).toEqual([...Object.keys(table), 'done'].sort())
  expect(Object.entries(states).filter(([, s]) => s.type === 'final').map(([k]) => k)).toEqual(['done'])
})

test('every outcome of every node has an edge, with its budgets left and spent', () => {
  for (const [node, outcomes] of Object.entries(table)) {
    for (const outcome of outcomes) {
      for (const context of [remain, spent]) expect(step(node, context, outcome), `${node} ${outcome}`).not.toBe('no edge')
    }
  }
})

test('every guard of the graph is named', () => {
  const states = delivery.toJSON().states as Record<string, { on?: Record<string, { guard?: unknown }[]> }>
  const guards = Object.values(states).flatMap((s) => Object.values(s.on ?? {}).flatMap((ts) => ts.flatMap((t) => (t.guard === undefined ? [] : [t.guard]))))
  expect(guards.length).toBeGreaterThan(0)
  for (const g of guards) expect(typeof g).toBe('string')
})

test('every budget takes its fix while it remains and ends in a park once it is spent', () => {
  expect(step('gate', remain, 'fail')).toBe('gate-fix')
  expect(step('gate', spent, 'fail')).toBe('parked failed')
  expect(step('review', remain, 'findings')).toBe('review-fix')
  expect(step('review', spent, 'findings')).toBe('pr')
  expect(step('ci', remain, 'checks-failed')).toBe('ci-fix')
  expect(step('ci', spent, 'checks-failed')).toBe('parked failed')
  expect(step('ci', spent, 'conflicts')).toBe('parked failed')
  expect(step('ci', remain, 'comments', 'bot')).toBe('address-reviews')
  expect(step('ci', spent, 'comments', 'bot')).toBe('parked failed')
  expect(step('ci', spent, 'comments', 'writer')).toBe('address-reviews')
})

test('the pr node goes to ci on opened, found and finished, and parks failed', () => {
  for (const outcome of ['opened', 'found', 'finished']) expect(step('pr', remain, outcome)).toBe('ci')
  expect(step('pr', remain, 'failed')).toBe('parked failed')
  expect(step('pr', remain, 'merged')).toBe('no edge')
})

test('a green pull request parks ready, and a yolo process whose panel passed is done', () => {
  expect(step('ci', remain, 'green')).toBe('parked ready')
  expect(step('ci', { ...remain, yolo: true }, 'green')).toBe('parked ready')
  expect(step('ci', { ...remain, yolo: true, panelPassed: true }, 'green')).toBe('done')
})

test("a state's meta carries its stage and its start and end events", () => {
  const meta = delivery.resolveState({ value: 'pr', context: remain }).getMeta()['delivery.pr'] as { stage: string; start: string; end: string; note: string }
  expect(meta).toMatchObject({ stage: 'pr', start: 'pr-start', end: 'pr-end', note: 'the author session writes the pull request' })
})

// recordOf is a work process with the history, the overrides and the mode given, in a checkout without settings.
const recordOf = (change: Partial<StageRecord>): StageRecord => ({ id: 'owner-repo-1', kind: 'work', project: '/nonexistent', stage: 'ci', state: 'running', note: '', history: [], ...change }) as StageRecord

test('the context of a record without history or overrides holds the default budgets, none spent', () => {
  expect(deliveryContext(recordOf({}))).toEqual({ gateFixes: 0, gateRounds: 3, reviewRound: 0, reviewRounds: 3, repairs: 0, repairRounds: 3, yolo: false, panelPassed: false })
})

test('the context reads the spent budgets from the history, the knobs from the overrides and the mode and panel', () => {
  const at = '2026-01-01T00:00:00Z'
  const record = recordOf({
    env: { WF_GATE_ROUNDS: '5', WF_REVIEW_ROUNDS: '2', WF_CI_REPAIR_ROUNDS: '1' },
    mode: 'yolo',
    panel: 'pass',
    history: [
      { stage: 'implement', kind: 'session', result: 'complete', at, session_id: 'i' },
      { stage: 'gate', kind: 'session', result: 'complete', at, session_id: 'g1' },
      { stage: 'gate', kind: 'session', result: 'complete', at, session_id: 'g2' },
      { stage: 'review', kind: 'round', result: 'fix', at, round: 1 },
      { stage: 'review', kind: 'round', result: 'pass', at, round: 2 },
      { stage: 'pr', kind: 'open', result: 'opened', at },
      { stage: 'ci', kind: 'session', result: 'complete', at, session_id: 'c1' },
    ],
  } as Partial<StageRecord>)
  expect(deliveryContext({ ...record, history: (record.history ?? []).slice(0, 3) }).gateFixes).toBe(2)
  expect(deliveryContext(record)).toEqual({ gateFixes: 0, gateRounds: 5, reviewRound: 2, reviewRounds: 2, repairs: 1, repairRounds: 1, yolo: true, panelPassed: true })
})

test('a knob that is no whole number reads as a budget of 0', () => {
  const context = deliveryContext(recordOf({ env: { WF_GATE_ROUNDS: 'many', WF_REVIEW_ROUNDS: '0', WF_CI_REPAIR_ROUNDS: '-1' } }))
  expect(context).toMatchObject({ gateRounds: 0, reviewRounds: 0, repairRounds: 0 })
})

test('a work and a hunt process run on the delivery graph, and an unknown kind is refused', () => {
  expect(graphOf(recordOf({})).machine).toBe(delivery)
  expect(graphOf(recordOf({ kind: 'hunt' } as Partial<StageRecord>))).toBe(graphOf(recordOf({})))
  expect(() => graphOf(recordOf({ kind: 'plan' } as unknown as Partial<StageRecord>))).toThrow('no process graph is registered for a plan process')
})

test('the repair guard parks failed on both edges once the rounds are spent, and a writer starts afresh', () => {
  const last = { ...remain, repairs: 2 }
  for (const outcome of ['checks-failed', 'conflicts']) {
    expect(step('ci', last, outcome)).toBe('ci-fix')
    expect(step('ci', spent, outcome)).toBe('parked failed')
  }
  expect(step('ci', last, 'comments', 'bot')).toBe('address-reviews')
  expect(step('ci', spent, 'comments', 'bot')).toBe('parked failed')
  expect(step('ci', spent, 'comments', 'writer')).toBe('address-reviews')
})

test('the ci fix and address-reviews sessions return to ci, and a follow-up waits on ci again', () => {
  expect(step('ci-fix', remain, 'complete')).toBe('ci')
  expect(step('address-reviews', spent, 'complete')).toBe('ci')
  expect(step('ci', spent, 'follow-up')).toBe('ci')
})

test('a green pull request whose yolo merge did not happen parks ready', () => {
  const snapshot = delivery.resolveState({ value: 'ci', context: { ...remain, yolo: true, panelPassed: true } })
  const [next, actions] = transition(delivery, snapshot, { type: 'green', unmerged: true })
  expect(String(next.value)).toBe('ci')
  expect(actions).toContainEqual(expect.objectContaining({ type: 'park', params: { state: 'ready' } }))
})
