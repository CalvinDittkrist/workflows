// The delivery graph: the process graph of a work process, an XState machine of its nodes and the edges
// their outcomes take. A node runs one stage, or one fix session of a stage, and returns an outcome. The
// graph names where the outcome goes. A park (ready, blocked, input, failed) is no state of its own. It is
// the action park on an edge, which keeps the process on its node until a later event takes an edge from
// there. Only done is final.
//
// The guards are named and read the budgets of today (budgets.ts). They read them from a context that the
// engine builds from the record at every transition (deliveryContext), so no budget is kept a second time.
// A state's meta carries its stage, the fields and the note the engine writes as it enters it, and its
// start and end event names. The engine (engine.ts) runs the machine through its pure functions only, and
// the registry (graphs.ts) pairs it with its nodes. This module imports the budgets and the settings, and
// no stage.
import { setup } from 'xstate'
import { gateFixesSpent, repairsSpent, reviewRounds } from './budgets.js'
import type { StateMeta } from './engine.js'
import type { StageRecord } from './records.js'
import { knob } from './settings.js'

// The budgets of today's stages, by their default.
const defaultGateRounds = 3
const defaultReviewRounds = 3
const defaultRepairRounds = 3

// What the guards of the delivery graph read, built from the record at every transition.
export type DeliveryContext = {
  // gateFixes are the fix sessions the gate spent, of gateRounds (WF_GATE_ROUNDS).
  gateFixes: number
  gateRounds: number
  // reviewRound is the number of the last round of the review, of reviewRounds (WF_REVIEW_ROUNDS).
  reviewRound: number
  reviewRounds: number
  // repairs are the repair rounds the ci stage spent on the pull request, of repairRounds (WF_CI_REPAIR_ROUNDS).
  repairs: number
  repairRounds: number
  // yolo and panelPassed say a green pull request is merged at once.
  yolo: boolean
  panelPassed: boolean
}

// budget reads a knob of the record, or 0 where it is not a whole number: the stage refuses such a knob
// itself before it returns an outcome a guard reads.
function budget(record: StageRecord, name: string, fallback: number, min: number): number {
  try {
    return knob(record, name, fallback, min)
  } catch {
    return 0
  }
}

// deliveryContext is the context of the delivery graph for the record as it stands.
export function deliveryContext(record: StageRecord): DeliveryContext {
  const history = record.history ?? []
  const rounds = reviewRounds(history)
  return {
    gateFixes: gateFixesSpent(history),
    gateRounds: budget(record, 'WF_GATE_ROUNDS', defaultGateRounds, 0),
    reviewRound: rounds.at(-1)?.round ?? rounds.length,
    reviewRounds: budget(record, 'WF_REVIEW_ROUNDS', defaultReviewRounds, 1),
    repairs: repairsSpent(history),
    repairRounds: budget(record, 'WF_CI_REPAIR_ROUNDS', defaultRepairRounds, 0),
    yolo: record.mode === 'yolo',
    panelPassed: record.panel === 'pass',
  }
}

// An event of the delivery graph: the outcome of a node, or a message or a follow-up to a parked
// process. mandate is writer or bot on a comments event of the ci stage: a writer's comments are always
// addressed, a bot's only while a repair round remains. unmerged is set on a green event whose yolo
// merge was refused or queued, which parks the process ready.
export type DeliveryEvent = { type: string; mandate?: 'writer' | 'bot'; unmerged?: boolean }

// park keeps the process on its node in one of the park states.
const park = (state: 'ready' | 'blocked' | 'input' | 'failed') => ({ type: 'park', params: { state } }) as const

// The parks every session node has: a session that asks, is blocked or failed waits on its node.
const sessionParks = { input: { actions: park('input') }, blocked: { actions: park('blocked') }, failed: { actions: park('failed') } }

// session is the meta of a node that runs a session of the stage.
const session = (stage: string, entry: Record<string, unknown> = {}): StateMeta => ({ stage, entry, start: 'session-start', end: 'session-end' })

export const delivery = setup({
  types: { context: {} as DeliveryContext, events: {} as DeliveryEvent },
  guards: {
    gateRoundsRemain: ({ context }) => context.gateFixes < context.gateRounds,
    reviewRoundsRemain: ({ context }) => context.reviewRound < context.reviewRounds,
    repairRoundsRemain: ({ context }) => context.repairs < context.repairRounds,
    writerOrRepairRoundsRemain: ({ context, event }) => event.mandate === 'writer' || context.repairs < context.repairRounds,
    yoloPanelPassed: ({ context, event }) => context.yolo && context.panelPassed && event.unmerged !== true,
  },
  actions: {
    // park is read from the transition by the engine, which writes the park; it runs nothing itself.
    park: () => {},
  },
}).createMachine({
  id: 'delivery',
  initial: 'implement',
  context: { gateFixes: 0, gateRounds: defaultGateRounds, reviewRound: 0, reviewRounds: defaultReviewRounds, repairs: 0, repairRounds: defaultRepairRounds, yolo: false, panelPassed: false },
  states: {
    implement: {
      meta: session('implement'),
      on: { complete: 'gate', ...sessionParks, message: 'implement' },
    },
    gate: {
      meta: { stage: 'gate', entry: { state: 'running', fixing: false }, note: 'the gate starts', start: 'gate-start', end: 'gate-end', failure: 'the gate failed', what: 'its gate' } satisfies StateMeta,
      on: {
        pass: 'review',
        skipped: 'review',
        fail: [{ guard: 'gateRoundsRemain', target: 'gate-fix' }, { actions: park('failed') }],
        failed: { actions: park('failed') },
      },
    },
    'gate-fix': {
      meta: session('gate', { fixing: true }),
      on: { complete: 'gate', ...sessionParks },
    },
    review: {
      meta: { stage: 'review', entry: { state: 'running', fixing: false }, note: 'the reviewers run', end: 'review-end', start: 'review-start', failure: 'the review failed', what: 'its review' } satisfies StateMeta,
      on: {
        pass: 'pr',
        findings: [{ guard: 'reviewRoundsRemain', target: 'review-fix' }, { target: 'pr' }],
        failed: { actions: park('failed') },
      },
    },
    'review-fix': {
      meta: session('review', { fixing: true }),
      on: { complete: 'gate', ...sessionParks },
    },
    pr: {
      meta: {
        stage: 'pr',
        entry: { state: 'running', fixing: false, wait: undefined },
        note: 'the author session writes the pull request',
        start: 'pr-start',
        end: 'pr-end',
        failure: 'the pr stage failed',
        what: 'its pr stage',
      } satisfies StateMeta,
      on: { opened: 'ci', found: 'ci', finished: 'ci', failed: { actions: park('failed') } },
    },
    ci: {
      // The ci node writes its own note and its start event, which name the pull request it waits on.
      meta: { stage: 'ci', entry: { state: 'waiting', fixing: false }, end: 'ci-end', failure: 'the ci stage failed', what: 'its ci stage' } satisfies StateMeta,
      on: {
        green: [{ guard: 'yoloPanelPassed', target: 'done' }, { actions: park('ready') }],
        merged: { actions: park('blocked') },
        unmergeable: { actions: park('blocked') },
        answered: { actions: park('blocked') },
        closed: { actions: park('failed') },
        failed: { actions: park('failed') },
        'checks-failed': [{ guard: 'repairRoundsRemain', target: 'ci-fix' }, { actions: park('failed') }],
        conflicts: [{ guard: 'repairRoundsRemain', target: 'ci-fix' }, { actions: park('failed') }],
        comments: [{ guard: 'writerOrRepairRoundsRemain', target: 'address-reviews' }, { actions: park('failed') }],
        'follow-up': 'ci',
      },
    },
    'ci-fix': {
      meta: session('ci', { fixing: true }),
      on: { complete: 'ci', ...sessionParks },
    },
    'address-reviews': {
      meta: session('address-reviews', { fixing: true }),
      on: { complete: 'ci', ...sessionParks },
    },
    done: { type: 'final' },
  },
})
