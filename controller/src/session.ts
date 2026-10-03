// The sessions of a work process: Claude Code run headless through the Agent SDK in the process's
// worktree. The implement session implements the issue and commits, with the bundled worker plugin, and
// ends by reporting complete with its commits or blocked through a structured result. On complete the
// controller starts the gate stage (gate.ts), unless the maintainer holds the session open. A fix session
// of the gate, of the review or of the ci stage is a fresh session with a stage timeout that reports the
// same way. The complete of a fix session of the ci stage or of an address-reviews session is an outcome
// of its node of the delivery graph. The engine (engine.ts) follows that outcome back to the ci node
// (ci.ts). Every other complete goes to the gate. Every subagent of a stage is an agent run (agents.ts): a
// reviewer, the author session, the spec checker, an auditor or the apply session. The one runner,
// agents, starts each here beside the process's own session, side by side where there are several. The
// streams of the read-only ones stay out of the event log. Every session's end is an attempt in the
// record's history. Its stream goes into the process's event log and its session id into the record. A
// session that ends without a result, or a runtime that cannot start, ends the process as failed with
// the reason. A session the controller's stop cuts off ends the process as interrupted. A resume goes on
// with it by its session id when it has one, and starts a fresh session otherwise.
//
// The session takes its input as a stream, so the maintainer writes to it while it runs.
// A message is its next turn.
// A permission the classifier does not settle and a question of the session reach the controller
// through the SDK's permission callback. The session waits until the process page answers them.
// A message to a process whose session has ended resumes that session by its id.
//
// A plan process runs a planner session the same way, with the bundled planner plugin and the planner's
// start context in its brief. It reports no result: each turn it ends waits for the maintainer, whose
// next message resumes it.
//
// A hunt process runs its hunt session in place of the implement session, the worker on the hunt skill,
// and the hunt record stands where the issue stands in every brief after it (hunt.ts).
//
// The first prompt of each session is its brief (briefs.ts), and the plugins it loads are the bundle's
// (bundle.ts).
//
// This module holds begin, say, hold and answer, the session loop with its permission callback, the
// report schemas of its own session, the one runner of the agent runs, and the plugins and agent of a
// session. The registry of the running processes, with stop, stop all and recover, is running.ts; the
// settings of a session, its runtime environment, the knobs, the rules an allowance grants and the hook
// against a direct GitHub write are settings.ts.
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { join } from 'node:path'
import { type McpSdkServerConfigWithInstance, type PermissionResult, type PermissionUpdate, query, type SDKMessage } from '@anthropic-ai/claude-agent-sdk'
import { type Addressed, type AgentRun, type Ended, report } from './agents.js'
import { brief, planBrief, safeRef } from './briefs.js'
import { advance } from './engine.js'
import { gate } from './gate.js'
import { graphOf } from './graphs.js'
import { githubServer, githubTools } from './github.js'
import { hunted, refresh } from './hunt.js'
import { type Answer, context, detail, questions } from './conversation.js'
import { type Project, Refusal } from './project.js'
import type { Attempt, CreatedRecord, Fix, PlanRecord, SessionRecord, StageRecord } from './records.js'
import { busy, firstStage, Input, register, release, type Request, type Running, runningOf } from './running.js'
import { allowance, guard, knob, runtimeEnv, sessionScoped, settings } from './settings.js'
import { attempt, event, readRecord, update, warn } from './store.js'

export interface Runtime {
  // claude is the executable the SDK starts: the machine's claude, or the scripted one in fake mode.
  claude: string
  // plugins is the directory of the bundled plugins, one directory per plugin.
  plugins: string
  stateDir: string
  // fake says the controller runs in fake mode, where the gate fetches nothing from origin and the gate
  // on CI, the pr and the ci stages push nothing.
  fake: boolean
  // gh is the gh the pr and ci stages call: the machine's, or the scripted one in fake mode.
  gh: string
  // poll is how many milliseconds the gate on CI and the ci stage let pass between two readings of the
  // pull request: 30 s, and less in fake mode.
  poll: number
  // announce tells the maintainer that a process turned blocked, ready or failed (notify.ts).
  announce: Announce
}

// Announce is told of a process once it has turned blocked, ready or failed, with its record as it
// ended. A yolo process that ended ready is told of although its record is gone.
export type Announce = (record: SessionRecord) => void

// agentOf is the plugin whose agent a session of the record's kind runs: the planner's for a plan
// process, the worker's for a work process.
export const agentOf = (record: SessionRecord): 'planner' | 'worker' => (record.kind === 'plan' ? 'planner' : 'worker')

// sessionAgent is the agent a session of the record runs with, or undefined for a stage after implement
// or hunt, whose fresh session runs its own brief without the worker's agent and its pipeline.
export const sessionAgent = (record: SessionRecord): 'planner' | 'worker' | undefined =>
  record.kind !== 'plan' && record.stage !== firstStage(record) ? undefined : agentOf(record)

// sessionPlugins are the directories of the bundled plugins a session of the record's kind loads: the
// plugin of its agent first, then repo-standards, whose skills every session may call. The marketplace
// copies are switched off (see workSettings), so these are the only copies it loads.
export const sessionPlugins = (dir: string, record: SessionRecord): string[] => [join(dir, agentOf(record)), join(dir, 'repo-standards')]

// The result a fix session of the review reports through: the report, and what it did with each finding.
const fixReport = {
  ...report,
  properties: {
    ...report.properties,
    fixes: {
      type: 'array',
      description: 'one entry for every finding of the brief, by its id',
      items: {
        type: 'object',
        properties: {
          finding: { type: 'string', description: 'the id of the finding, such as code-1-2' },
          outcome: { type: 'string', enum: ['fixed', 'declined'] },
          note: { type: 'string', description: 'one line: what changed, or why it was declined' },
        },
        required: ['finding', 'outcome', 'note'],
        additionalProperties: false,
      },
    },
  },
  required: [...report.required, 'fixes'],
}

// The result an address-reviews session reports through: the report, its reply to each thread of its
// brief, its answer to the requests for changes, and the points it fixed and declined.
const addressReport = {
  ...report,
  properties: {
    ...report.properties,
    replies: {
      type: 'array',
      description: 'one reply to each thread of the brief, by its id, which the controller posts before it resolves the thread',
      items: {
        type: 'object',
        properties: {
          thread: { type: 'string', description: 'the id of a thread the brief lists' },
          body: { type: 'string', description: 'one or two sentences: what changed, or why not' },
        },
        required: ['thread', 'body'],
        additionalProperties: false,
      },
    },
    answer: { type: 'string', description: 'the answer to the requests for changes of the brief, point by point, which the controller posts as one comment; empty when it lists none' },
    fixed: { type: 'array', items: { type: 'string' }, description: 'the points fixed, one line each' },
    declined: { type: 'array', items: { type: 'string' }, description: 'the points declined, one line each with the reason' },
  },
  required: [...report.required, 'replies', 'answer', 'fixed', 'declined'],
}

// The tools a read-only agent run never has: it reads and reports, and changes nothing.
const readOnly = ['Edit', 'Write', 'MultiEdit', 'NotebookEdit', 'Agent']

// The stage timeout of a session after implement, in seconds, unless WF_STAGE_TIMEOUT says otherwise.
const stageTimeout = 1800

// sessionOf names a process's session in its notes, and stageOf is the stage the session runs.
const sessionOf = (record: SessionRecord) =>
  record.kind === 'plan'
    ? 'planner session'
    : record.stage === 'gate'
      ? 'fix session of the gate'
      : record.stage === 'review'
        ? 'fix session of the review'
        : record.stage === 'ci'
          ? 'fix session of the ci stage'
          : record.stage === 'address-reviews'
            ? 'address-reviews session'
            : `${firstStage(record)} session`
const stageOf = (record: SessionRecord) => (record.kind === 'plan' ? record.stage : ['gate', 'review', 'ci', 'address-reviews'].includes(record.stage) ? record.stage : firstStage(record))

// begin starts the session of a process, the implement session of a claimed work process or the planner
// session of a plan, and answers its record as it runs. A process with a session id resumes that session
// in its worktree. The session goes on after the answer; its end is written into the record. A work
// session that reports complete starts the gate stage, or opens the hold when one is set on implement.
// A message is the first turn of the session, in place of the brief.
export function begin(record: StageRecord | PlanRecord, project: Project, rt: Runtime, message?: string): SessionRecord {
  const id = record.id
  const resumed = record.session_id
  const what = sessionOf(record)
  const stage = stageOf(record)
  const note = resumed ? `${what} resumed` : `${what} running`
  const started = update(rt.stateDir, id, { state: 'running', stage, note, ...(record.kind !== 'plan' ? { held: undefined } : {}) } as Partial<CreatedRecord>) ?? record
  event(rt.stateDir, id, { event: 'session-start', stage, ...(resumed ? { resume: resumed } : {}) })
  const abort = new AbortController()
  const input = new Input()
  const requests = new Map<string, Request>()
  let exited: Promise<void> = Promise.resolve()
  const spawned = (p: Promise<void>) => (exited = p)
  const s: Running = { abort, done: Promise.resolve(), input, requests, over: false }
  const live = () => runningOf(id) === s && !s.over
  const end = ({ state, note, commits, session_id, fixes, addressed }: Ended) => {
    if (!live()) return
    s.over = true
    input.close()
    for (const [request, r] of requests) {
      r.close()
      event(rt.stateDir, id, { event: 'closed', request })
    }
    requests.clear()
    event(rt.stateDir, id, { event: 'session-end', stage, state, note, ...(commits ? { commits } : {}) })
    if (record.kind !== 'plan') {
      const sessionId = session_id ?? readRecord(rt.stateDir, id)?.session_id
      const a: Attempt = {
        stage: stage as Attempt['stage'],
        kind: 'session',
        result: state,
        at: new Date().toISOString(),
        note,
        ...(sessionId ? { session_id: sessionId } : {}),
        ...(commits ? { commits } : {}),
        ...(fixes ? { fixes } : {}),
        ...(stage === 'address-reviews' && record.addressing ? { mandate: record.addressing.mandate } : {}),
        ...(addressed ? { fixed: addressed.fixed, declined: addressed.declined } : {}),
      }
      if (state === 'complete') {
        const now = readRecord(rt.stateDir, id) as StageRecord | undefined
        // A held implement session stays open for more turns: the hold is spent, and the maintainer's next
        // message resumes it, whose next complete starts the gate.
        if (stage === 'implement' && now?.hold) {
          attempt(rt.stateDir, id, a, { hold: false, held: true, state: 'input', note: `complete, held open: ${note}; write to go on, and its next complete starts the gate`, unseen: true })
          return
        }
        // The gate starts once this session's runtime has exited, so two never work the worktree at once.
        // A fix session of the review goes to the gate too, whose pass starts the next round. A fix
        // session of the ci stage goes back to its wait, which pushes what it committed, and so does an
        // address-reviews session, whose replies and answer the wait posts once it has pushed.
        // Until then a stop of the next stage also stops this runtime, whose forced kill still applies.
        // What an address-reviews session reported is written with its end, so a restart before the ci
        // stage posted it resumes the stage with it.
        const addressing = stage === 'address-reviews' ? (now?.addressing ?? record.addressing) : undefined
        const reported = { replies: addressed?.replies ?? [], answer: addressed?.answer ?? '' }
        const done = attempt(rt.stateDir, id, a, { fixing: false, ...(addressing ? { addressing: { ...addressing, reported } } : {}) } as Partial<StageRecord>)
        // A hunt session's complete reads the hunt record, which decides between the gate and the end.
        if (done && (stage === 'ci' || stage === 'address-reviews')) advance(graphOf(done), stage === 'ci' ? 'ci-fix' : 'address-reviews', { outcome: 'complete' }, done, project, rt, exited, abort)
        else if (done && done.kind === 'hunt' && stage === 'hunt') hunted(done, project, rt, exited, abort)
        else if (done) gate(done, project, rt, exited, abort)
        return
      }
      attempt(rt.stateDir, id, a)
    }
    // The process is unseen until its page is opened, so the dashboard marks it until then. A planner
    // that waits for input is told on the board alone, as a question of a session is.
    const ended = update(rt.stateDir, id, { state, note, unseen: true })
    if (ended && state !== 'input') rt.announce(ended)
  }
  // A write that fails, as on a full or read-only disk, ends this process failed where it still can and
  // is told on stderr; it never reaches the controller as an unhandled rejection.
  const settle = (r: Ended) => {
    try {
      end(r)
    } catch (err) {
      warn(id, `could not write the end of its session (${r.state})`, err)
      try {
        s.over = true
        const failed = update(rt.stateDir, id, { state: 'failed', note: `could not write the end of the ${what}: ${(err as Error).message}`, unseen: true })
        if (failed) rt.announce(failed)
      } catch (again) {
        warn(id, 'could not mark it failed', again)
      }
    }
  }
  const repo = `${project.owner}/${project.name}`
  if (message !== undefined) input.push(message)
  else if (record.kind === 'plan') input.push(planBrief(record, repo, existsSync(join(record.worktree, 'docs', 'glossary.md'))))
  else input.push(brief(record, repo))
  register(id, s)
  s.done = session(record, rt, s, live, spawned, ownRun(record, s, rt, repo))
    .then(settle, (err: Error) => settle({ state: 'failed', note: `the ${what} failed: ${err.message}` }))
    .catch((err: unknown) => warn(id, 'its session ended unexpectedly', err))
    .then(() => exited)
    .finally(() => {
      release(id, s)
    })
  return started
}

// say writes the maintainer's message to the process's session and answers where it went.
// A question that waits takes it as its answer. A session that runs takes it as its next turn.
// A session that has ended is resumed by its id with the message.
export async function say(record: SessionRecord, text: string, rt: Runtime, project: () => Promise<Project>): Promise<'answered' | 'sent' | 'resumed'> {
  const id = record.id
  const s = runningOf(id)
  if (s?.busy && !s.over) throw new Refusal(`${s.busy} and no session runs to write to; write once they have ended`, 409)
  if (s && !s.over) {
    const question = [...s.requests.values()].find((r) => r.kind === 'question')
    if (question) {
      question.answer({ text })
      return 'answered'
    }
    event(rt.stateDir, id, { event: 'message', text })
    s.input.push(text)
    return 'sent'
  }
  // A session that is over is let exit before it is resumed, so two never run at once.
  if (s) await s.done
  const p = await project()
  // Another message may have resumed the session meanwhile; this one is then its next turn.
  if (busy(id)) return say(record, text, rt, project)
  const now = readRecord(rt.stateDir, id)
  if (!now) throw new Refusal(`${id} is not a process of this machine`, 404)
  if (!now.session_id) throw new Refusal('the process has no session to write to yet; wait until its session has started', 409)
  // A standardize process runs its apply session once per apply, which an apply again starts afresh.
  if (now.kind === 'standardize') throw new Refusal('the apply session of this standardize process has ended; apply again to start it afresh', 409)
  event(rt.stateDir, id, { event: 'message', text })
  // A follow-up to a ready work process is new work on it: its session goes on as the implement session,
  // whose complete runs the gate and a review with every reviewer again. A ready hunt goes on as its hunt
  // session the same way.
  // A message to a work process the ci stage left blocked has its session take the review on, a fix
  // session of the ci stage, which a restart of the controller resumes as such.
  const next =
    now.kind !== 'plan' && now.state === 'ready' && now.stage !== firstStage(now)
      ? (update(rt.stateDir, id, { stage: firstStage(now), fixing: false, panel: undefined } as Partial<CreatedRecord>) ?? now)
      : now.kind !== 'plan' && now.stage === 'ci' && !now.fixing
        ? (update(rt.stateDir, id, { fixing: true } as Partial<CreatedRecord>) ?? now)
        : now
  begin(next as StageRecord | PlanRecord, p, rt, text)
  return 'resumed'
}

// hold sets whether the next complete report of a work process's implement session keeps the session
// open instead of starting the gate. It refuses a process that is not a work process in implement.
export function hold(stateDir: string, record: SessionRecord, on: boolean): StageRecord {
  if (record.kind !== 'work') throw new Refusal(`${record.id} is no work process; only an implement session is held`, 409)
  if (on && record.stage !== 'implement') throw new Refusal(`${record.id} is in the stage ${record.stage}, past implement; only an implement session is held`, 409)
  const done = update(stateDir, record.id, { hold: on } as Partial<CreatedRecord>)
  if (!done) throw new Refusal(`${record.id} is not a process of this machine`, 404)
  return done as StageRecord
}

// answer answers a permission request of the process's session.
export function answer(id: string, request: string, a: Answer) {
  const r = runningOf(id)?.requests.get(request)
  if (!r || r.kind !== 'permission') throw new Refusal(`no permission request ${request} waits in ${id}; it was answered, or its session has ended`, 409)
  r.answer(a)
}

// A run of a session: its input and abort, and how it runs and reports. The process's own session is
// one; an agent run is another, which runs beside it and, unless it writes, leaves the record alone.
interface Run {
  input: Input
  abort: AbortController
  // what names the session in notes, stage the stage the scripted claude of fake mode plays by.
  what: string
  stage?: string
  agent?: string
  resume?: string
  // later says it runs after implement, a fresh session with the stage timeout.
  later: boolean
  // own says it is the process's own session, whose stream, session id and context the record follows.
  own: boolean
  schema?: Record<string, unknown>
  disallowed?: string[]
  // tools are the controller's in-process tools the session writes GitHub with, allowed without a card.
  tools?: McpSdkServerConfigWithInstance
  // read reads the structured result the session reported.
  read: (out: unknown, sessionId: string | undefined) => Ended
}

// ownRun is the run of a process's own session: a work session reports complete or blocked, a fix
// session of the review also what it did with each finding, and a planner session reports nothing.
// A planner session writes GitHub through the controller's tools alone (ADR 0059), and every write goes
// into the process's event log.
function ownRun(record: StageRecord | PlanRecord, s: Running, rt: Runtime, repo: string): Run {
  const what = sessionOf(record)
  const review = record.kind !== 'plan' && record.stage === 'review'
  const address = record.kind !== 'plan' && record.stage === 'address-reviews'
  return {
    input: s.input,
    abort: s.abort,
    what,
    ...(record.kind !== 'plan' ? { stage: stageOf(record) } : {}),
    agent: sessionAgent(record),
    resume: record.session_id,
    // A stage after implement or hunt runs a fresh session of its own brief, without the worker's agent
    // and its pipeline, and with the stage timeout.
    later: record.kind !== 'plan' && record.stage !== firstStage(record),
    own: true,
    ...(record.kind === 'plan'
      ? { tools: githubTools(rt.gh, repo, (e) => event(rt.stateDir, record.id, e)) }
      : { schema: review ? fixReport : address ? addressReport : report }),
    read: (raw, sessionId) => {
      const out = raw as { outcome?: unknown; message?: unknown; commits?: unknown; fixes?: unknown } | undefined
      const commits = Array.isArray(out?.commits) ? out.commits.filter((c): c is string => typeof c === 'string') : []
      if (out && (out.outcome === 'complete' || out.outcome === 'blocked') && typeof out.message === 'string') {
        return {
          state: out.outcome,
          note: out.message,
          session_id: sessionId,
          commits,
          ...(review ? { fixes: fixesOf(out.fixes) } : {}),
          ...(address ? { addressed: addressedOf(out) } : {}),
        }
      }
      return { state: 'failed', note: `the ${what} ended without a report of complete or blocked`, session_id: sessionId }
    },
  }
}

// fixesOf reads the fixes a fix session of the review reported, leaving out what has not their shape.
function fixesOf(raw: unknown): Fix[] {
  if (!Array.isArray(raw)) return []
  return raw.flatMap((f: unknown) => {
    const x = f as Partial<Fix> | null
    if (!x || typeof x.finding !== 'string' || (x.outcome !== 'fixed' && x.outcome !== 'declined')) return []
    return [{ finding: x.finding, outcome: x.outcome, note: typeof x.note === 'string' ? x.note : '' }]
  })
}

// addressedOf reads what an address-reviews session reported, leaving out what has not its shape.
function addressedOf(raw: unknown): Addressed {
  const out = raw as { replies?: unknown; answer?: unknown; fixed?: unknown; declined?: unknown }
  const lines = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])
  const replies = (Array.isArray(out.replies) ? out.replies : []).flatMap((r: unknown) => {
    const x = r as { thread?: unknown; body?: unknown } | null
    return x && typeof x.thread === 'string' && typeof x.body === 'string' ? [{ thread: x.thread, body: x.body }] : []
  })
  return { replies, answer: typeof out.answer === 'string' ? out.answer : '', fixed: lines(out.fixed), declined: lines(out.declined) }
}

// A start of an agent run: the run and the brief the caller passes it.
export interface Start {
  run: AgentRun
  brief: string
}

// agents is the one runner of the subagents (agents.ts). It starts each run in starts as a fresh session
// with the stage timeout, in parallel with the process's own session. Once every runtime has exited, it
// answers how each ended, in the order given. A read-only run runs in the default mode with the
// read-only tools denied; a writing run runs as the process's own session, in the auto mode with every
// tool. s is the process's entry of the stage, whose abort stops them all and which holds their requests;
// own tells them apart from a stop.
export async function agents<T extends Start[]>(record: SessionRecord, rt: Runtime, s: Running, own: () => boolean, starts: [...T]): Promise<{ [K in keyof T]: Ended }> {
  const exits: Promise<void>[] = []
  const ends = await Promise.all(starts.map((start) => aside(record, rt, s, own, start, exits)))
  await Promise.all(exits)
  return ends as { [K in keyof T]: Ended }
}

// aside runs one agent run beside the process's own session and answers how it ended. exits is told of
// its runtime's exit.
async function aside(record: SessionRecord, rt: Runtime, s: Running, own: () => boolean, { run: a, brief }: Start, exits: Promise<void>[]): Promise<Ended> {
  const abort = new AbortController()
  const all = () => abort.abort()
  // A parent stopped already ends the session at once; the forwarding stays until its runtime exits.
  if (s.abort.signal.aborted) abort.abort()
  else s.abort.signal.addEventListener('abort', all, { once: true })
  let exited: Promise<void> = Promise.resolve()
  const input = new Input()
  input.push(brief)
  const run: Run = {
    input,
    abort,
    what: a.name,
    stage: a.stage,
    ...(a.agent ? { agent: a.agent } : {}),
    later: true,
    own: a.writes,
    schema: a.schema,
    ...(a.writes ? {} : { disallowed: readOnly }),
    read: a.read,
  }
  try {
    return await session(record, rt, s, own, (p) => {
      exited = p
      exits.push(p)
    }, run)
  } catch (err) {
    return { state: 'failed', note: `the ${a.name} failed: ${(err as Error).message}` }
  } finally {
    input.close()
    void exited.finally(() => s.abort.signal.removeEventListener('abort', all))
  }
}

async function session(
  record: SessionRecord,
  rt: Runtime,
  s: Running,
  live: () => boolean,
  spawned: (exited: Promise<void>) => void,
  run: Run,
): Promise<Ended> {
  const id = record.id
  // A planner session's own turn ends in a wait; a session beside it, as the spec checker, reports.
  const plan = record.kind === 'plan' && run.own
  const { what, later, agent } = run
  const plugins = sessionPlugins(rt.plugins, record)
  const missing = plugins.find((path) => !existsSync(join(path, '.claude-plugin', 'plugin.json')))
  if (missing) return { state: 'failed', note: `the bundled plugin is missing at ${missing}; reinstall ameise` }
  // The brief names the branch and the base in commands the session runs; a name from origin with a
  // shell character in it does not reach the prompt.
  for (const name of [record.branch, record.base]) {
    if (!safeRef.test(name)) return { state: 'failed', note: `the branch name ${JSON.stringify(name)} has characters the brief does not carry; rename it on origin` }
  }

  // waiting shows the process as waiting for the maintainer while a request of its session waits.
  // A question goes before a permission. Once none waits, the process is running again.
  const waiting = () => {
    if (!live()) return
    const open = [...s.requests.values()]
    const first = open.find((r) => r.kind === 'question') ?? open[0]
    if (!first) update(rt.stateDir, id, { state: 'running', note: `${what} running` })
    else update(rt.stateDir, id, { state: first.kind === 'question' ? 'input' : 'approval', note: first.note, unseen: true })
  }
  // ask records a request and waits for its answer, or for its session to end without one.
  const ask = (request: string, r: Omit<Request, 'answer' | 'close'>, e: Record<string, unknown>, decide: (a: Answer | { text: string }) => PermissionResult, signal: AbortSignal) =>
    new Promise<PermissionResult>((resolve) => {
      const closed: PermissionResult = { behavior: 'deny', message: 'The session ended before the maintainer answered.' }
      const settle = (result: PermissionResult) => {
        if (!s.requests.delete(request)) return
        resolve(result)
        waiting()
      }
      s.requests.set(request, {
        ...r,
        answer: (a) => {
          const result = decide(a)
          event(rt.stateDir, id, { event: 'answer', request, ...(typeof a === 'string' ? { answer: a } : { text: a.text }) })
          settle(result)
        },
        close: () => {
          s.requests.delete(request)
          resolve(closed)
        },
      })
      event(rt.stateDir, id, { request, ...e })
      waiting()
      signal.addEventListener(
        'abort',
        () => {
          if (!s.requests.has(request)) return
          event(rt.stateDir, id, { event: 'closed', request })
          settle(closed)
        },
        { once: true },
      )
    })

  const canUseTool = async (
    tool: string,
    input: Record<string, unknown>,
    o: { signal: AbortSignal; suggestions?: PermissionUpdate[]; toolUseID: string; requestId: string; title?: string; description?: string; decisionReason?: string; blockedPath?: string },
  ): Promise<PermissionResult> => {
    const request = o.toolUseID || o.requestId
    if (tool === 'AskUserQuestion') {
      const asked = questions(input)
      return ask(request, { kind: 'question', note: asked[0]?.question ?? 'The session asks a question' }, { event: 'question', questions: asked }, (a) => {
        const text = typeof a === 'string' ? a : a.text
        return { behavior: 'allow', updatedInput: { ...input, answers: Object.fromEntries(asked.map((q) => [q.question, text])) } }
      }, o.signal)
    }
    const keys = allowance(tool, input, o.suggestions)
    // A reviewer neither uses nor keeps the process's allowances: a grant for one call of a reviewer
    // widens neither the process's own session nor another reviewer.
    const allowed = run.own ? (readRecord(rt.stateDir, id)?.allowed ?? []) : []
    const shown = detail(tool, input, record.worktree)
    const title = o.title || `${tool} wants to run`
    // A call the maintainer allowed for this process is allowed again without a card.
    if (keys.every((k) => allowed.includes(k))) {
      event(rt.stateDir, id, { event: 'allowed', tool, detail: shown })
      return { behavior: 'allow', updatedInput: input }
    }
    const reason = [o.decisionReason || o.description || '', o.blockedPath ? `It reaches ${o.blockedPath}.` : ''].filter(Boolean).join(' ')
    return ask(request, { kind: 'permission', note: shown ? `${title}: ${shown}` : title }, { event: 'permission', tool, detail: shown, title, reason }, (a) => {
      if (typeof a !== 'string' || a === 'deny') return { behavior: 'deny', message: 'The maintainer denied this call in the process view.' }
      if (a === 'once' || !run.own) return { behavior: 'allow', updatedInput: input }
      const now = readRecord(rt.stateDir, id)?.allowed ?? []
      update(rt.stateDir, id, { allowed: [...now, ...keys.filter((k) => !now.includes(k))] })
      return { behavior: 'allow', updatedInput: input, updatedPermissions: sessionScoped(o.suggestions) }
    }, o.signal)
  }

  let timeout: number | undefined
  if (later) {
    try {
      timeout = knob(record, 'WF_STAGE_TIMEOUT', stageTimeout, 1)
    } catch (err) {
      return { state: 'failed', note: (err as Error).message }
    }
  }
  let timedOut = false
  const timer = timeout === undefined ? undefined : setTimeout(() => {
    timedOut = true
    run.abort.abort()
  }, timeout * 1000)
  timer?.unref()
  const late = (): Ended => ({ state: 'failed', note: `the ${what} ran past its stage timeout of ${timeout} s` })

  let stderr = ''
  const q = query({
    prompt: run.input,
    options: {
      abortController: run.abort,
      cwd: record.worktree,
      ...(run.resume ? { resume: run.resume } : {}),
      pathToClaudeCodeExecutable: rt.claude,
      // AMEISE_STAGE names the stage the session runs, which the scripted claude of fake mode plays by.
      env: { ...runtimeEnv(), ...(run.stage ? { AMEISE_STAGE: run.stage } : {}) },
      plugins: plugins.map((path) => ({ type: 'local' as const, path })),
      settingSources: ['user', 'project', 'local'],
      settings: settings(record),
      ...(agent ? { agent } : {}),
      ...(run.disallowed ? { disallowedTools: run.disallowed } : {}),
      ...(run.tools ? { mcpServers: { [githubServer]: run.tools }, allowedTools: [`mcp__${githubServer}`], hooks: { PreToolUse: [{ matcher: 'Bash', hooks: [guard(rt.stateDir, id)] }] } } : {}),
      // A reviewer runs in the default mode: the runtime lets through the calls it knows read only, and
      // every other call is a card, where auto mode would let its classifier allow a write.
      permissionMode: run.own ? 'auto' : 'default',
      canUseTool,
      extraArgs: { 'strict-mcp-config': null },
      // A planner session reports nothing: its turns end in a question to the maintainer.
      ...(run.schema ? { outputFormat: { type: 'json_schema' as const, schema: run.schema } } : {}),
      // The controller starts the runtime itself, so a stop can wait for its exit.
      spawnClaudeCodeProcess: (o) => {
        const child = spawn(o.command, o.args, { cwd: o.cwd, env: o.env, signal: o.signal, stdio: ['pipe', 'pipe', 'pipe'] })
        child.stderr.on('data', (d: Buffer) => (stderr = (stderr + d.toString()).slice(-2000)))
        spawned(
          new Promise<void>((exit) => {
            if (child.exitCode !== null || child.signalCode !== null) return exit()
            child.once('exit', () => exit())
            child.once('error', () => exit())
            // A runtime that outlives the SDK's grace after a stop is killed.
            run.abort.signal.addEventListener('abort', () => setTimeout(() => child.kill('SIGKILL'), 10000).unref(), { once: true })
          }),
        )
        return child
      },
    },
  })
  let sessionId = run.resume
  let size = record.context
  const lastLine = () => stderr.trim().split('\n').pop()
  try {
    for await (const message of q as AsyncIterable<SDKMessage>) {
      if (timedOut) return late()
      if (!live()) break
      if (run.own) event(rt.stateDir, id, { event: 'stream', message })
      if (sessionId === undefined && typeof message.session_id === 'string' && message.session_id !== '') {
        sessionId = message.session_id
        if (run.own) update(rt.stateDir, id, { session_id: sessionId })
      }
      const c = run.own ? context(message) : undefined
      if (c !== undefined && c !== size) {
        size = c
        update(rt.stateDir, id, { context: c })
      }
      // A hunt session's tool results are where its hunt record changes, so the record follows them.
      if (run.own && record.kind === 'hunt' && record.stage === 'hunt' && message.type === 'user') refresh(record, rt)
      if (message.type !== 'result') continue
      // A message the maintainer wrote while the turn ran makes a turn of its own after this one.
      if (message.subtype === 'success' && (message.queued_turn_count ?? 0) > 0) continue
      run.input.close()
      if (message.subtype !== 'success') return { state: 'failed', note: `the ${what} ended with ${message.subtype}`, session_id: sessionId }
      if (plan) return { state: 'input', note: waitNote(message.result) }
      return run.read(message.structured_output, sessionId)
    }
  } catch (err) {
    if (timedOut) return late()
    // The runtime's own last word on stderr says why it stopped, which the SDK's error leaves out.
    const last = lastLine()
    throw new Error(`${(err as Error).message}${last ? `: ${last}` : ''}`, { cause: err })
  } finally {
    clearTimeout(timer)
  }
  if (timedOut) return late()
  const last = lastLine()
  return { state: 'failed', note: `the ${what} exited without a result${last ? `: ${last}` : ''}` }
}

// waitNote is the note of a planner that ended its turn: the last line of what it said, which is
// most often its question, or a plain wait when it said nothing.
function waitNote(result: string): string {
  const last = result.trim().split('\n').filter((l) => l.trim() !== '').pop()?.trim() ?? ''
  if (last === '') return 'the planner waits for your answer'
  return last.length > 200 ? `${last.slice(0, 199)}…` : last
}
