// The ci stage of a work process, which the controller runs once the pr stage has opened the pull
// request. It pushes what the branch has, then waits on the pull request, one wait at a time and with no
// session polling.
// It waits first for GitHub to say whether the branch merges into its base, then for the checks.
// Then it waits for a review of a bot of WF_PR_BOT_REVIEWERS, within WF_PR_REVIEW_WAIT seconds of the
// checks' end. Last it reads the standing requests for changes and the unresolved threads.
// A bot's review is a review of it in any state, or its thumbs-up reaction on the pull request.
// Codex leaves that reaction instead of a review when it finds nothing.
//
// A gate's draft the pr stage marked ready waits for the checks its ready starts, as the README's ci
// stage says. Bot reviewers skip drafts, so their review is waited for from the ready on.
//
// The stage runs as three nodes of the delivery graph (delivery.ts), and the engine (engine.ts) follows
// their outcomes. No node starts a stage or names its successor.
//
// The ci node pushes, posts what an address-reviews session reported, and waits. It returns the verdict
// as its outcome.
// A conflict or failed checks go to the ci fix node while WF_CI_REPAIR_ROUNDS remain.
// The ci fix node runs a fresh fix session. Its complete comes back to the ci node.
// Review points no round has answered yet go to the address-reviews node. Such a point is a writer's
// request for changes, or an unresolved thread a writer or a bot opened.
// The address-reviews session fixes or declines each point and reports its replies. Its complete comes
// back to the ci node.
// The ci node then posts the replies, resolves their threads and answers the requests in one comment.
// A writer's request starts the repair count afresh, once. A bot's review costs a repair round.
// A request asked again costs one too, so sessions never answer a request without end.
// The repair guard of the graph reads the same count.
//
// Green parks the process ready, where the board offers the merge.
// A yolo process whose panel passed is merged at once, by the merge action's rules, and is done.
// Three verdicts are never green and block the process until the maintainer answers:
// - a request that stands once answered,
// - a request or thread of somebody who is no writer,
// - a merge state other than clean.
// A pull request merged meanwhile blocks the process too, for the maintainer to abandon.
// A spent repair budget parks the process failed, and so does a closed pull request.
// A bot's points with the budget spent get a comment that names the writers first.
// Every verdict other than a wait is an attempt in the record's history.
//
// followUps reads the pull request of each process the stage left ready or blocked on a review.
// It waits on it again once a new request for changes or thread asks for an answer, a review stands on
// a ready one, or a review it was blocked on has changed. That follow-up is an event on the ci node.
import { readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { merge } from './actions.js'
import { fetch, git, push } from './git.js'
import { run } from './exec.js'
import { defaultGrace } from './gate.js'
import type { Project } from './project.js'
import { addressBrief, ciFixBrief } from './briefs.js'
import { repairsSpent } from './budgets.js'
import { advance, type Node, type NodeContext, type Outcome } from './engine.js'
import { graphOf } from './graphs.js'
import { busy, track } from './running.js'
import { begin, type Runtime } from './session.js'
import { knob, setting } from './settings.js'
import type { Attempt, Check, Point, StageRecord } from './records.js'
import { event, readRecord, recordsDir, update } from './store.js'

// The ci stage's knobs, the worker's own defaults: the repair rounds of one pull request, the bots whose
// review is waited for, and how many seconds after the checks' end a bot's review is waited for.
const defaultRepairs = 3
const defaultBots = 'chatgpt-codex-connector'
const defaultReviewWait = 1200
// How many seconds after the stage started an empty rollup reads as checks GitHub has not registered
// yet, in a repository that has workflows.
const checksGrace = 600

// botsOf reads WF_PR_BOT_REVIEWERS of the process: a comma-separated list of logins, written with or
// without [bot]; empty means no bot is waited for, and unset means the worker's default.
export function botsOf(record: StageRecord): string[] {
  const value = setting(record, 'WF_PR_BOT_REVIEWERS')
  if (value === undefined) return [defaultBots]
  if (typeof value !== 'string') throw new Error(`WF_PR_BOT_REVIEWERS=${String(value)} is not a list of logins; set it as such, such as ${defaultBots}, or empty for no bot`)
  return [...new Set(value.split(',').map((l) => l.trim().replace(/\[bot\]$/, '')).filter((l) => l !== ''))]
}

// A reading of the pull request as gh pr view answers it: the fields the verdict is made of.
export interface Reading {
  number: number
  url: string
  state: string
  headRefOid?: string
  mergeable: string
  mergeStateStatus?: string
  statusCheckRollup?: { name?: string; context?: string; conclusion?: string | null; state?: string | null; status?: string | null; completedAt?: string | null; detailsUrl?: string; targetUrl?: string }[]
  reviews?: { id?: string; author?: { login?: string } | null; authorAssociation?: string; body?: string; url?: string; state: string; submittedAt?: string }[]
}

// A reaction on the pull request: its content, such as THUMBS_UP, and the login of who reacted. The
// GraphQL API names a bot only among the reactors of a reaction group, since Reaction.user is a User.
export interface Reaction {
  content?: string
  login?: string
}

// A reaction group of the pull request as the GraphQL API answers it: its content and who reacted with it,
// a bot, a user, an organization or a mannequin.
interface ReactionGroup {
  content?: string
  reactors?: { nodes?: ({ login?: string } | null)[] }
}

// A review thread as the GraphQL API answers it: its id, whether it is resolved, where it is, and its
// first comments with who wrote them.
export interface Thread {
  id?: string
  isResolved: boolean
  path?: string | null
  line?: number | null
  comments?: { nodes?: { author?: { __typename?: string; login?: string } | null; authorAssociation?: string; body?: string; url?: string }[] }
}

// The associations of an account that may be a writer: GitHub puts them on a review or a comment of a
// member of the organisation, of somebody invited to the repository and of its owner. None of them is
// write access, so each author of these is asked for their push permission, and only one who may push is
// a writer, whose request for changes is a mandate. Anybody may review a public repository, and what a
// review says becomes the brief of a session that pushes, so nobody else's review is answered by a session.
const members = ['OWNER', 'MEMBER', 'COLLABORATOR']
// How much of one review's or thread's words the brief of an address-reviews session carries.
const maxWords = 4000
const cut = (text: string, n: number) => (text.length > n ? `${text.slice(0, n - 1)}…` : text)

// Points are what the reviews of a reading ask for: the requests for changes of writers and the
// unresolved threads a writer or a bot opened, which a session may answer, and one line for each of
// everything else that stands, which only a person answers.
export interface Points {
  asks: Point[]
  others: string[]
  // lines are every request and the count of the unresolved threads, one line each, as the record shows them.
  lines: string[]
}

// pointsOf reads the points of a reading and of its unresolved threads. What one writer says is their
// latest review that states anything; a request for changes stands until they approve or it is dismissed.
// pushers are the keys of the reviews and comments whose author may push, as pushersOf reads them.
export function pointsOf(r: Reading, threads: Thread[], pushers: Set<string>): Points {
  const latest = new Map<string, NonNullable<Reading['reviews']>[number]>()
  for (const v of r.reviews ?? []) {
    if (v.state === 'COMMENTED' || v.state === 'PENDING') continue
    const login = v.author?.login ?? 'someone'
    if ((latest.get(login)?.submittedAt ?? '') <= (v.submittedAt ?? '')) latest.set(login, v)
  }
  const asks: Point[] = []
  const others: string[] = []
  const lines: string[] = []
  for (const [login, v] of latest) {
    if (v.state !== 'CHANGES_REQUESTED') continue
    lines.push(`${login} requested changes`)
    const key = reviewKey(v)
    if (!pushers.has(key)) {
      others.push(`${login} requested changes and is no writer of the repository`)
      continue
    }
    asks.push({ kind: 'request', key, login, body: cut((v.body ?? '').trim(), maxWords), ...(v.url ? { url: v.url } : {}) })
  }
  const open = threads.filter((t) => !t.isResolved)
  if (open.length > 0) lines.push(`${open.length} review thread(s) not resolved`)
  for (const t of open) {
    const comments = t.comments?.nodes ?? []
    const first = comments[0]
    const counts = (c: (typeof comments)[number], i: number) => c.author?.__typename === 'Bot' || pushers.has(commentKey(t, c, i))
    const where = `${t.path ?? 'the pull request'}${t.line ? `:${t.line}` : ''}`
    if (!t.id || !first || !counts(first, 0)) {
      others.push(`the thread on ${where} was opened by ${first?.author?.login ?? 'somebody'}, who is no writer and no bot`)
      continue
    }
    // Only what writers and bots said in it goes into a brief.
    const said = comments.filter(counts).map((c) => `@${c.author?.login ?? 'someone'}: ${(c.body ?? '').trim()}`)
    asks.push({
      kind: 'thread',
      key: t.id,
      login: first.author?.login ?? 'someone',
      body: cut(said.join('\n'), maxWords),
      where,
      bot: first.author?.__typename === 'Bot',
      ...(first.url ? { url: first.url } : {}),
    })
  }
  return { asks, others, lines }
}

type Review = NonNullable<Reading['reviews']>[number]
type Comment = NonNullable<NonNullable<Thread['comments']>['nodes']>[number]
// The key of a review and of a comment of a thread, by which whether its author may push is asked once.
const reviewKey = (v: Review) => v.id ?? `${v.author?.login ?? 'someone'}@${v.submittedAt ?? ''}`
const commentKey = (t: Thread, c: Comment, i: number) => c.url ?? `${t.id ?? '?'}#${i}`

// pushed is whether the author of a review or comment may push, by its repository and key. A review or
// a comment is written once, and the access its author had then is what it stands on, as in the factory.
const pushed = new Map<string, boolean>()

// mayPush asks GitHub whether login may push to the repository.
async function mayPush(gh: string, repo: string, login: string): Promise<boolean> {
  const out = JSON.parse(await run(gh, ['api', `repos/${repo}/collaborators/${encodeURIComponent(login)}/permission`])) as { user?: { permissions?: { push?: boolean } } }
  return out.user?.permissions?.push === true
}

// pushersOf are the keys of the requests for changes and the comments of unresolved threads of a reading
// whose author may push to the repository. Only an author GitHub names a member, a collaborator or the
// owner is asked; a bot is none, and counts by being a bot. A question GitHub does not answer throws.
export async function pushersOf(gh: string, repo: string, r: Reading, threads: Thread[]): Promise<Set<string>> {
  const asked: { key: string; login: string }[] = []
  for (const v of r.reviews ?? []) {
    if (v.state === 'CHANGES_REQUESTED' && v.author?.login && members.includes(v.authorAssociation ?? '')) asked.push({ key: reviewKey(v), login: v.author.login })
  }
  for (const t of threads.filter((t) => !t.isResolved)) {
    ;(t.comments?.nodes ?? []).forEach((c, i) => {
      if (c.author?.__typename !== 'Bot' && c.author?.login && members.includes(c.authorAssociation ?? '')) asked.push({ key: commentKey(t, c, i), login: c.author.login })
    })
  }
  const pushers = new Set<string>()
  for (const { key, login } of asked) {
    const known = `${repo}:${key}`
    let may = pushed.get(known)
    if (may === undefined) {
      try {
        may = await mayPush(gh, repo, login)
      } catch (err) {
        throw new Error(`whether ${login} may push to ${repo} could not be read: ${(err as Error).message.split('\n')[0]}`, { cause: err })
      }
      pushed.set(known, may)
    }
    if (may) pushers.add(key)
  }
  return pushers
}

// answeredOf are the keys of the requests an answer of the history commented on and of the threads it
// replied to: what no later round answers again.
export const answeredOf = (history: Attempt[]): Set<string> => new Set(history.flatMap((h) => (h.kind === 'answer' ? [...(h.answered ?? []), ...(h.replied ?? [])] : [])))

// askedOf are the keys of the points an address-reviews session of the history was given to answer.
export const askedOf = (history: Attempt[]): Set<string> => new Set(history.flatMap((h) => (h.stage === 'ci' && h.kind === 'wait' ? (h.asked ?? []) : [])))

const readingFields = 'number,url,state,headRefOid,mergeable,mergeStateStatus,statusCheckRollup,reviews'

const failures = ['FAILURE', 'ERROR', 'CANCELLED', 'TIMED_OUT', 'ACTION_REQUIRED', 'STARTUP_FAILURE']
const pendings = ['PENDING', 'EXPECTED', 'QUEUED', 'IN_PROGRESS', 'WAITING', 'REQUESTED']

// checksOf are the checks of a reading, each pass, fail or pending, with when it completed.
export function checksOf(r: Reading): (Check & { completed?: number })[] {
  return (r.statusCheckRollup ?? []).map((c) => {
    const s = c.conclusion || c.state || 'PENDING'
    const pending = pendings.includes(s) || (c.status != null && c.status !== 'COMPLETED')
    const state: Check['state'] = failures.includes(s) ? 'fail' : pending ? 'pending' : 'pass'
    const url = c.detailsUrl || c.targetUrl
    const completed = c.completedAt ? Date.parse(c.completedAt) : NaN
    return { name: c.name || c.context || 'check', ...(url ? { url } : {}), state, ...(Number.isFinite(completed) ? { completed } : {}) }
  })
}

// The verdict of one reading: a wait with what it waits for, or an end of the wait.
type Verdict =
  | { kind: 'waiting'; wait: string }
  | { kind: 'closed' | 'merged' | 'conflicts' | 'checks-failed' | 'green' }
  | { kind: 'review-comments'; reviews: string[]; points: Points }
  | { kind: 'unmergeable'; status: string }

interface Knobs {
  bots: string[]
  reviewWait: number
  workflows: boolean
  started: number
  // readied is when the pr stage marked the gate's draft ready, onReady says a workflow runs on that,
  // and grace is WF_CHECKS_GRACE in seconds.
  readied?: number
  onReady: boolean
  grace: number
}

// judge makes the verdict of one reading, in the order of the waits. doneAt is when the checks were first
// seen done without GitHub saying when, which the review wait counts from; it is the caller's, across
// readings. points reads the points of the reading, which are asked only once every wait before it has passed.
// reactions reads the reactions on the pull request, which are asked only while the review wait stands.
async function judge(r: Reading, points: () => Promise<Points>, reactions: () => Promise<Reaction[]>, k: Knobs, now: number, doneAt: { at?: number }): Promise<Verdict> {
  if (r.state === 'MERGED') return { kind: 'merged' }
  if (r.state !== 'OPEN') return { kind: 'closed' }
  if (r.mergeable === 'CONFLICTING') return { kind: 'conflicts' }
  if (r.mergeable !== 'MERGEABLE') return { kind: 'waiting', wait: 'GitHub to say whether the branch merges into its base' }
  const checks = checksOf(r)
  const pending = checks.filter((c) => c.state === 'pending').length
  if (pending > 0) return { kind: 'waiting', wait: `the checks: ${pending} of ${checks.length} pending` }
  if (checks.length === 0 && k.workflows && now - k.started < checksGrace * 1000) return { kind: 'waiting', wait: 'GitHub to register the checks of the workflows' }
  if (checks.some((c) => c.state === 'fail')) return { kind: 'checks-failed' }
  const last = Math.max(0, ...checks.map((c) => c.completed ?? 0))
  if (k.readied !== undefined && k.onReady && last <= k.readied && now - k.readied < k.grace * 1000) {
    return { kind: 'waiting', wait: `the checks of marking the draft ready for review, until ${new Date(k.readied + k.grace * 1000).toISOString()}` }
  }
  if (last > 0) doneAt.at = last
  else doneAt.at ??= now
  // A bot reviews no draft, so its wait starts at the ready at the earliest.
  if (k.readied !== undefined) doneAt.at = Math.max(doneAt.at, k.readied)
  // A bot's review is a review of it in any state, or its thumbs up on the pull request, on whichever commit.
  const bot = (login?: string) => k.bots.includes((login ?? '').replace(/\[bot\]$/, ''))
  const reviewed = (r.reviews ?? []).some((v) => bot(v.author?.login))
  if (k.bots.length > 0 && !reviewed && now - doneAt.at < k.reviewWait * 1000 && !(await reactions()).some((x) => x.content === 'THUMBS_UP' && bot(x.login))) {
    return { kind: 'waiting', wait: `a review of ${k.bots.join(', ')}, until ${new Date(doneAt.at + k.reviewWait * 1000).toISOString()}` }
  }
  const read = await points()
  if (read.lines.length > 0) return { kind: 'review-comments', reviews: read.lines, points: read }
  // The merge takes only a clean pull request: one behind its base or blocked by a rule of it is not green.
  const status = r.mergeStateStatus ?? 'UNKNOWN'
  if (status === 'UNKNOWN') return { kind: 'waiting', wait: 'GitHub to say whether the base lets the branch merge' }
  if (status !== 'CLEAN') return { kind: 'unmergeable', status }
  return { kind: 'green' }
}

// threadsOf reads the first hundred review threads of a pull request, with their first twenty comments.
// With reactions it also reads who reacted to the pull request, the first hundred of each content, in the
// same query, through the reactors of the reaction groups, which name bots as well.
export async function threadsOf(gh: string, owner: string, name: string, n: number, reactions = false): Promise<{ threads: Thread[]; reactions: Reaction[] }> {
  const thumbs = reactions ? ' reactionGroups{content reactors(first:100){nodes{__typename ... on Bot{login} ... on User{login} ... on Organization{login} ... on Mannequin{login}}}}' : ''
  const query = `query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){reviewThreads(first:100){nodes{id isResolved path line comments(first:20){nodes{author{__typename login} authorAssociation body url}}}}${thumbs}}}}`
  const out = JSON.parse(await run(gh, ['api', 'graphql', '-F', `owner=${owner}`, '-F', `name=${name}`, '-F', `number=${n}`, '-f', `query=${query}`])) as {
    data?: { repository?: { pullRequest?: { reviewThreads?: { nodes?: Thread[] }; reactionGroups?: ReactionGroup[] | null } } }
  }
  const pull = out.data?.repository?.pullRequest
  const nodes = pull?.reviewThreads?.nodes
  if (!nodes) throw new Error(`GitHub named no review threads of PR #${n}`)
  if (reactions && !pull?.reactionGroups) throw new Error(`GitHub named no reactions of PR #${n}`)
  const found = (pull?.reactionGroups ?? []).flatMap((g) => (g.reactors?.nodes ?? []).map((x) => ({ content: g.content, login: x?.login })))
  return { threads: nodes, reactions: found }
}

// readPoints reads the review threads of a reading of pull request n, unless they are given, and whose
// authors may push, into its points.
export async function readPoints(gh: string, owner: string, name: string, n: number, r: Reading, given?: Thread[]): Promise<Points> {
  const threads = given ?? (await threadsOf(gh, owner, name, n)).threads
  return pointsOf(r, threads, await pushersOf(gh, `${owner}/${name}`, r, threads))
}

// workflowsIn says whether the worktree has workflows of GitHub Actions, so an empty rollup is checks
// GitHub has not registered yet and not green.
function workflowsIn(wt: string): boolean {
  try {
    return readdirSync(join(wt, '.github', 'workflows')).some((f) => f.endsWith('.yml') || f.endsWith('.yaml'))
  } catch {
    return false
  }
}

// readyIn says a workflow of the worktree names the ready_for_review event, so marking a draft ready
// starts checks of its own. It reads the text, not the YAML: a workflow that only mentions the event
// costs a wait of the checks grace, never a green on checks that were not there yet.
function readyIn(wt: string): boolean {
  try {
    const dir = join(wt, '.github', 'workflows')
    return readdirSync(dir)
      .filter((f) => f.endsWith('.yml') || f.endsWith('.yaml'))
      .some((f) => readFileSync(join(dir, f), 'utf8').includes('ready_for_review'))
  } catch {
    return false
  }
}

// pause lets ms pass, or less once the signal aborts.
export function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve()
    const done = () => {
      clearTimeout(timer)
      signal.removeEventListener('abort', done)
      resolve()
    }
    const timer = setTimeout(done, ms)
    signal.addEventListener('abort', done, { once: true })
  })
}

type Reported = NonNullable<NonNullable<StageRecord['addressing']>['reported']>

// The mutations that reply to a review thread and resolve it.
const replyMutation = 'mutation($threadId:ID!,$body:String!){addPullRequestReviewThreadReply(input:{pullRequestReviewThreadId:$threadId,body:$body}){comment{url}}}'
const resolveMutation = 'mutation($threadId:ID!){resolveReviewThread(input:{threadId:$threadId}){thread{isResolved}}}'
// maxReply bounds one reply or answer, below GitHub's 65536 characters for a comment.
const maxReply = 60000

// post carries the replies and the answer of an address-reviews session to GitHub: a reply and a
// resolution per thread its brief listed, the first reply to each, and one comment for the requests.
// A reply to a thread the brief did not list is posted nowhere, since the session's writing may have
// been steered by what it read. What cannot be posted is a note; a thread whose reply failed is asked
// again by the next round. It answers the attempt that records what it posted.
async function post(record: StageRecord, project: Project, rt: Runtime, n: number, url: string, got: Reported): Promise<Attempt> {
  const repo = `${project.owner}/${project.name}`
  const shown = record.addressing?.points ?? []
  const threads = new Map(shown.filter((p) => p.kind === 'thread').map((p) => [p.key, p]))
  const replied: string[] = []
  const notes: string[] = []
  for (const reply of got.replies) {
    const t = threads.get(reply.thread)
    const body = reply.body.trim()
    if (!t) {
      notes.push(`the address-reviews session replied to the thread ${JSON.stringify(reply.thread)}, which its brief did not list, so nothing was posted there`)
      continue
    }
    if (body === '') {
      notes.push(`the address-reviews session gave the thread on ${t.where ?? '?'} an empty reply, so nothing was posted there`)
      continue
    }
    threads.delete(reply.thread)
    try {
      await run(rt.gh, ['api', 'graphql', '-f', `threadId=${t.key}`, '-f', `body=${cut(body, maxReply)}`, '-f', `query=${replyMutation}`])
    } catch (err) {
      notes.push(`the reply to the thread on ${t.where ?? '?'} could not be posted: ${(err as Error).message.split('\n')[0]}`)
      continue
    }
    replied.push(t.key)
    try {
      await run(rt.gh, ['api', 'graphql', '-f', `threadId=${t.key}`, '-f', `query=${resolveMutation}`])
    } catch (err) {
      notes.push(`the thread on ${t.where ?? '?'} has its reply but could not be resolved; resolve it on GitHub: ${(err as Error).message.split('\n')[0]}`)
    }
  }
  const requests = shown.filter((p) => p.kind === 'request')
  const answered: string[] = []
  const answer = got.answer.trim()
  if (requests.length > 0 && answer === '') notes.push(`the address-reviews session gave no answer to the review of ${requests.map((p) => p.login).join(', ')}, so nothing was commented`)
  else if (requests.length > 0) {
    const file = join(recordsDir(rt.stateDir), `${record.id}.answer.md`)
    try {
      writeFileSync(file, cut(answer, maxReply))
      await run(rt.gh, ['pr', 'comment', String(n), '--repo', repo, '--body-file', file])
      answered.push(...requests.map((p) => p.key))
    } catch (err) {
      notes.push(`the answer to the review of ${requests.map((p) => p.login).join(', ')} could not be commented: ${(err as Error).message.split('\n')[0]}`)
    } finally {
      rmSync(file, { force: true })
    }
  }
  for (const note of notes) event(rt.stateDir, record.id, { event: 'ci-note', note })
  return {
    stage: 'address-reviews',
    kind: 'answer',
    result: notes.length > 0 ? 'partial' : 'posted',
    at: new Date().toISOString(),
    pr: n,
    url,
    answered,
    replied,
    ...(notes.length > 0 ? { note: notes.join('; ') } : {}),
  }
}

// mention comments on the pull request that the repair budget is spent, and mentions the writers whose
// points still stand, never a bot, whose mention may start a review of its own. A comment that cannot be
// posted is a note.
async function mention(record: StageRecord, project: Project, rt: Runtime, n: number, repairs: number, asks: Point[]) {
  const logins = [...new Set(asks.filter((p) => !p.bot).map((p) => p.login))]
  const who = logins.length > 0 ? `${logins.map((l) => `@${l}`).join(' ')} ` : ''
  const text = `${who}The controller spent the ${repairs} repair round(s) of this pull request and answers no more of its review on its own: ${asks.length} point(s) are left to a person.`
  const file = join(recordsDir(rt.stateDir), `${record.id}.answer.md`)
  try {
    writeFileSync(file, text)
    await run(rt.gh, ['pr', 'comment', String(n), '--repo', `${project.owner}/${project.name}`, '--body-file', file])
  } catch (err) {
    event(rt.stateDir, record.id, { event: 'ci-note', note: `could not comment on PR #${n} that the repair budget is spent: ${(err as Error).message.split('\n')[0]}` })
  } finally {
    rmSync(file, { force: true })
  }
}

// ciNode is the ci node of the delivery graph (delivery.ts). It pushes and posts what an address-reviews
// session reported. Then it waits on the pull request and returns its verdict. It starts no stage and no
// session itself; the engine follows its outcome. A stop ends its wait; a resume enters the node again.
export const ciNode: Node = {
  run: async (ctx) => {
    try {
      return await wait(ctx)
    } catch (err) {
      // A throw parks the process failed, and its wait is over.
      if (ctx.own()) update(ctx.rt.stateDir, ctx.record.id, { wait: undefined } as Partial<StageRecord>)
      throw err
    }
  },
}

// stopped is what a node returns once a stop has taken the process over, which the engine discards.
const stopped: Outcome = { outcome: 'stopped' }

// repairRounds is WF_CI_REPAIR_ROUNDS of the process, or its default where the ci node refused it already.
function repairRounds(record: StageRecord): number {
  try {
    return knob(record, 'WF_CI_REPAIR_ROUNDS', defaultRepairs)
  } catch {
    return defaultRepairs
  }
}

async function wait({ record, project, rt, signal, running, own, attempt: nodeAttempt, event: nodeEvent }: NodeContext): Promise<Outcome> {
  const id = record.id
  const repo = `${project.owner}/${project.name}`
  const wt = record.worktree
  const first = record.pull?.number
  running.busy = `the ci stage waits on PR #${first ?? '?'}`
  update(rt.stateDir, id, { note: `waiting on PR #${first ?? '?'}` })
  nodeEvent({ event: 'ci-start', stage: 'ci', pr: first })
  // end is an outcome that ends the wait, with the attempt of its verdict.
  const end = (outcome: string, note?: string, a?: Attempt, fields: Partial<Outcome> = {}): Outcome => {
    if (!own()) return stopped
    const change = { wait: undefined } as Partial<StageRecord>
    if (a) nodeAttempt(a, change)
    else update(rt.stateDir, id, change)
    return { outcome, ...(note !== undefined ? { note } : {}), ...fields }
  }
  const pull = record.pull
  if (!pull) return end('failed', 'the ci stage has no pull request to wait on; resume it to open one')
  const n = pull.number
  let repairs: number
  let k: Knobs
  try {
    repairs = knob(record, 'WF_CI_REPAIR_ROUNDS', defaultRepairs)
    const readied = record.readied ? Date.parse(record.readied) : NaN
    k = {
      bots: botsOf(record),
      reviewWait: knob(record, 'WF_PR_REVIEW_WAIT', defaultReviewWait),
      workflows: workflowsIn(wt),
      started: Date.now(),
      ...(Number.isFinite(readied) ? { readied } : {}),
      onReady: readyIn(wt),
      grace: knob(record, 'WF_CHECKS_GRACE', defaultGrace),
    }
  } catch (err) {
    return end('failed', (err as Error).message)
  }

  // What a fix session committed reaches the pull request first.
  try {
    await push(wt, record.branch, rt.fake)
  } catch (err) {
    return end('failed', `could not push ${record.branch} to origin: ${(err as Error).message}`)
  }
  if (!own()) return stopped
  // The replies and the answer of an address-reviews session go to GitHub once what it fixed is pushed.
  const reported = record.addressing?.reported
  if (reported) {
    const posted = await post(record, project, rt, n, pull.url, reported)
    if (!own()) return stopped
    nodeAttempt(posted, { addressing: undefined } as Partial<StageRecord>)
  }
  const head = await git(wt, 'rev-parse', 'HEAD')
  const now = () => new Date().toISOString()
  const doneAt: { at?: number } = {}
  let shown = ''
  let seen = ''
  const history = () => (readRecord(rt.stateDir, id) as StageRecord | undefined)?.history ?? record.history ?? []
  update(rt.stateDir, id, { repairs: { spent: repairsSpent(history()), of: repairs } } as Partial<StageRecord>)
  for (;;) {
    if (!own()) return stopped
    let verdict: Verdict
    let checks: Check[] = []
    try {
      const r = JSON.parse(await run(rt.gh, ['pr', 'view', String(n), '--repo', repo, '--json', readingFields])) as Reading
      checks = checksOf(r).map((c) => ({ name: c.name, ...(c.url ? { url: c.url } : {}), state: c.state }))
      // A reading of another head is GitHub's before the push has reached it.
      if (r.headRefOid && r.headRefOid !== head && r.state === 'OPEN') verdict = { kind: 'waiting', wait: `GitHub to show the push of ${head.slice(0, 7)}` }
      else {
        // The reactions are read only when the review wait asks for them, with the threads in one query,
        // and the threads reuse that query; a reading that asks only for the threads reads no reactions.
        let pulled: ReturnType<typeof threadsOf> | undefined
        const graph = (reactions: boolean) => (pulled ??= threadsOf(rt.gh, project.owner, project.name, n, reactions))
        verdict = await judge(
          r,
          async () => readPoints(rt.gh, project.owner, project.name, n, r, (await graph(false)).threads),
          async () => (await graph(true)).reactions,
          k,
          Date.now(),
          doneAt,
        )
      }
    } catch (err) {
      verdict = { kind: 'waiting', wait: `GitHub to answer: ${(err as Error).message.split('\n')[0]}` }
    }
    if (!own()) return stopped
    const said = JSON.stringify(checks)
    if (verdict.kind === 'waiting') {
      if (verdict.wait !== shown || said !== seen) {
        shown = verdict.wait
        seen = said
        nodeEvent({ event: 'ci-wait', stage: 'ci', pr: n, wait: verdict.wait })
        update(rt.stateDir, id, { state: 'waiting', note: `PR #${n}: waiting for ${verdict.wait}`, wait: verdict.wait, checks } as Partial<StageRecord>)
      }
      await pause(rt.poll, signal)
      continue
    }
    update(rt.stateDir, id, { checks } as Partial<StageRecord>)
    const a: Attempt = { stage: 'ci', kind: 'wait', result: verdict.kind, at: now(), commit: head, pr: n, url: pull.url, checks }
    if (verdict.kind === 'green') return green(record, project, rt, n, a, own, nodeAttempt)
    if (verdict.kind === 'merged') return end('merged', `PR #${n} is merged already on GitHub; abandon the process to remove its worktree and branch`, a)
    if (verdict.kind === 'unmergeable') return end('unmergeable', `PR #${n} is not green: its merge state is ${verdict.status}, not CLEAN; meet the base's rules on GitHub, or write here to have the session bring the branch up to date`, a)
    if (verdict.kind === 'closed') return end('closed', `PR #${n} is closed, so there is nothing to wait on; open it again and resume, or abandon the process`, a)
    const spent = repairsSpent(history())
    if (verdict.kind === 'review-comments') {
      a.reviews = verdict.reviews
      const answered = answeredOf(history())
      const asks = verdict.points.asks.filter((p) => !answered.has(p.key))
      if (asks.length === 0) {
        // Every point a session may answer has its answer: what stands waits for the reviewer or a person.
        const again = verdict.points.asks.filter((p) => p.kind === 'request').map((p) => p.login)
        const stands = [...(again.length > 0 ? [`the request for changes of ${again.join(', ')} is answered and waits for their review again`] : []), ...verdict.points.others]
        const what = (stands.length > 0 ? stands : verdict.reviews).join('; ')
        a.result = again.length > 0 && verdict.points.others.length === 0 ? 'answered' : 'review-comments'
        return end('answered', `PR #${n} is not green: ${what}; answer the review, or write here to have the session take it on`, a)
      }
      // A writer's request no session was asked yet is a new mandate, which starts the repair count
      // afresh; a bot's review, a thread without a request, or a request asked again is a repair round of
      // its own.
      const asked = askedOf(history())
      const fresh = asks.filter((p) => p.kind === 'request' && !asked.has(p.key))
      const again = asks.filter((p) => p.kind === 'request' && asked.has(p.key))
      const mandate = fresh.length > 0 ? 'writer' : 'bot'
      nodeEvent({ event: 'ci', ...a })
      // A bot's points with the budget spent are left to a person, whom the spent comment names.
      if (mandate === 'bot' && spent >= repairs) {
        await mention(record, project, rt, n, repairs, asks)
        return end('comments', `the ci stage spent its ${repairs} repair round(s): ${asks.length} review point(s) on PR #${n} are left to a person`, a, { mandate })
      }
      if (!own()) return stopped
      // a.asked records the points the address-reviews node answers,
      // so a later wait does not treat them as unanswered.
      a.asked = asks.map((p) => p.key)
      const note =
        mandate === 'writer'
          ? `answering the request for changes of ${[...new Set(fresh.map((p) => p.login))].join(', ')}; the repair count starts afresh`
          : again.length > 0
            ? `answering the request for changes of ${[...new Set(again.map((p) => p.login))].join(', ')} again, and ${asks.length - again.length} review thread(s); repair round ${spent + 1} of ${repairs}`
            : `answering ${asks.length} review thread(s); repair round ${spent + 1} of ${repairs}`
      if (!nodeAttempt(a, { wait: undefined, note, addressing: { mandate, points: asks } } as Partial<StageRecord>)) return stopped
      return { outcome: 'comments', mandate }
    }
    const what = whatFailed(record, n, verdict.kind, checks)
    nodeEvent({ event: 'ci', ...a })
    return end(verdict.kind, `the ci stage spent its ${repairs} repair round(s): ${what}`, a)
  }
}

// whatFailed names a conflict of the pull request with its base, or the checks that failed on it.
const whatFailed = (record: StageRecord, n: number, kind: string, checks: Check[]) =>
  kind === 'conflicts'
    ? `PR #${n} conflicts with ${record.base}`
    : `checks failed on PR #${n}: ${checks
        .filter((c) => c.state === 'fail')
        .map((c) => c.name)
        .join(', ')}`

// ciFixNode is the fix-session node of the ci stage, a fresh session. Its brief is for the conflict or
// for the failed checks of the verdict it was entered with. That verdict is the last wait of the record. Before a
// conflict fix it fetches the base. Its complete returns to the ci node through the engine.
export const ciFixNode: Node = {
  adapt: (record, project, rt) => {
    const id = record.id
    const history = record.history ?? []
    const verdict = [...history].reverse().find((h) => h.stage === 'ci' && h.kind === 'wait')
    const kind = verdict?.result === 'conflicts' ? 'conflicts' : 'checks-failed'
    const checks = verdict?.checks ?? []
    const n = record.pull?.number ?? 0
    const repairs = repairRounds(record)
    const spent = repairsSpent(history)
    const what = whatFailed(record, n, kind, checks)
    // A fix session is a fresh session: the one before it is in the history, not in its resume.
    const fixing =
      (update(rt.stateDir, id, { stage: 'ci', session_id: undefined, fixing: true, wait: undefined, note: `${what}; repair round ${spent + 1} of ${repairs}`, repairs: { spent: spent + 1, of: repairs } } as Partial<StageRecord>) as
        | StageRecord
        | undefined) ?? record
    const start = () => begin(fixing, project, rt, ciFixBrief(fixing, `${project.owner}/${project.name}`, n, kind, checks.filter((c) => c.state === 'fail')))
    if (kind !== 'conflicts' || !record.base.startsWith('origin/')) return start()
    // The base is fetched before the conflict fix, tracked so a stop meanwhile starts no session.
    const abort = new AbortController()
    let own = () => false
    const done = fetch(record.project, record.base.slice('origin/'.length), rt.fake)
      .catch(() => false)
      .then((fetched) => {
        if (!own()) return
        if (!fetched) event(rt.stateDir, id, { event: 'ci-note', note: `could not fetch ${record.base}; the fix session merges what this checkout has of it` })
        start()
      })
      .catch((err: unknown) => {
        process.stderr.write(`warning: ${id}: its ci fix session could not start: ${(err as Error).message}\n`)
      })
    own = track(id, abort, done, `the ci stage fetches ${record.base}`).own
    return fixing
  },
}

// addressNode is the address-reviews node: a fresh session with the points of its record, the
// writer's request or a bot's review the wait gave it. What the session reported is written with its
// end, and its complete returns to the ci node through the engine, which posts it.
export const addressNode: Node = {
  adapt: (record, project, rt) => {
    const addressing = record.addressing
    const points = addressing?.points ?? []
    const repairs = repairRounds(record)
    const spent = repairsSpent(record.history ?? [])
    const started =
      (update(rt.stateDir, record.id, {
        stage: 'address-reviews',
        session_id: undefined,
        fixing: true,
        wait: undefined,
        repairs: { spent: addressing?.mandate === 'writer' ? 0 : spent + 1, of: repairs },
      } as Partial<StageRecord>) as StageRecord | undefined) ?? record
    return begin(started, project, rt, addressBrief(started, `${project.owner}/${project.name}`, record.pull?.number ?? 0, points))
  },
}

// green is the outcome of a green pull request. A manual process parks ready for the maintainer's
// merge. A yolo process whose panel passed is merged at once, by the merge action's rules. The merge
// removes its worktree, its branch and its record, so it ends done. The maintainer is told of it as it
// ended. A
// merge that is refused, or that a merge queue takes, is unmerged and parks it ready with the reason.
async function green(record: StageRecord, project: Project, rt: Runtime, n: number, a: Attempt, own: () => boolean, attempt: NodeContext['attempt']): Promise<Outcome> {
  const note = `PR #${n} is green: it merges, its checks pass and no review asks for changes`
  const change = { wait: undefined } as Partial<StageRecord>
  if (record.mode !== 'yolo' || record.panel !== 'pass') {
    if (!own()) return stopped
    attempt(a, change)
    return { outcome: 'green', note: record.mode !== 'yolo' ? note : `${note}; its panel did not pass, so the yolo process waits for the merge` }
  }
  const id = record.id
  const before = attempt(a, { ...change, note: `${note}; merging it, as the process is yolo` } as Partial<StageRecord>)
  if (!before) return stopped
  event(rt.stateDir, id, { event: 'ci-note', note: `merging PR #${n}, as the process is yolo` })
  let merged: Awaited<ReturnType<typeof merge>>
  try {
    merged = await merge(project, rt.stateDir, rt.gh, rt.fake, n)
  } catch (err) {
    return { outcome: 'green', note: `${note}, but its yolo merge was refused: ${(err as Error).message}; merge it by hand`, unmerged: true }
  }
  if (merged.queued) return { outcome: 'green', note: `${note}; the merge queue of ${merged.base} took it, and the process stays until GitHub merges it`, unmerged: true }
  const closed = merged.closed !== null ? `, closed #${merged.closed}` : ''
  const warned = merged.warnings.length > 0 ? `; ${merged.warnings.join('; ')}` : ''
  // The merge removed the record, so the maintainer is told of the process as it was, merged.
  rt.announce({ ...before, state: 'ready', note: `merged PR #${n} into ${merged.base}${closed}, as the process is yolo${warned}`, wait: undefined } as StageRecord)
  return { outcome: 'green' }
}

// Whether a process the ci stage left calls for a follow-up: one ready, or blocked on a review, whose
// pull request the stage waits on again once its reviews ask for an answer or have changed.
const following = (r: StageRecord) =>
  (r.kind === 'work' || r.kind === 'hunt') &&
  r.stage === 'ci' &&
  r.pull !== undefined &&
  (r.state === 'ready' || (r.state === 'blocked' && ['review-comments', 'answered'].includes(r.history?.at(-1)?.result ?? '') && r.history?.at(-1)?.kind === 'wait'))

// warned is the last warning of each process's follow-up, so one that keeps failing is told once. A
// follow-up that reads its pull request again forgets it.
const warned = new Map<string, string>()

// followUps reads the pull request of every process the ci stage left ready or blocked on a review once,
// and starts the stage again for each whose reviews ask for an answer no round gave, for one ready,
// whose reviews stand at all, or, for one blocked, whose reviews say something other than what it was
// blocked on. A pull request that cannot
// be read is read again next time. projectOf derives the project of a checkout.
export async function followUps(rt: Runtime, projectOf: (path: string) => Promise<Project>): Promise<void> {
  let names: string[]
  try {
    names = readdirSync(recordsDir(rt.stateDir)).filter((f) => f.endsWith('.json'))
  } catch {
    return
  }
  for (const name of names) {
    const id = name.slice(0, -'.json'.length)
    try {
      const r = readRecord(rt.stateDir, id) as StageRecord | undefined
      if (!r || !following(r) || busy(id)) continue
      const project = await projectOf(r.project)
      const n = r.pull!.number
      const reading = JSON.parse(await run(rt.gh, ['pr', 'view', String(n), '--repo', `${project.owner}/${project.name}`, '--json', readingFields])) as Reading
      if (reading.state !== 'OPEN') continue
      const points = await readPoints(rt.gh, project.owner, project.name, n, reading)
      warned.delete(id)
      const answered = answeredOf(r.history ?? [])
      const fresh = points.asks.some((p) => !answered.has(p.key))
      // A ready process was green with no review standing, so any that stands now, even one no session
      // may answer, is a change; a blocked one has changed once its reviews say something else.
      const changed = r.state === 'ready' ? points.lines.length > 0 : JSON.stringify(points.lines) !== JSON.stringify(r.history?.at(-1)?.reviews ?? [])
      if (!fresh && !changed) continue
      // The record is read again, as a click may have changed it while GitHub answered.
      const now = readRecord(rt.stateDir, id) as StageRecord | undefined
      if (!now || !following(now) || busy(id)) continue
      event(rt.stateDir, id, { event: 'ci-note', note: fresh ? `a review of PR #${n} asks for an answer: the follow-up waits on it again` : now.state === 'ready' ? `a review of PR #${n} stands since it was green: the follow-up waits on it again` : `the review PR #${n} was blocked on has changed: the follow-up waits on it again` })
      advance(graphOf(now), 'ci', { outcome: 'follow-up' }, now, project, rt)
    } catch (err) {
      const why = (err as Error).message.split('\n')[0] ?? ''
      if (warned.get(id) !== why) process.stderr.write(`warning: ${id}: its follow-up could not read its pull request: ${why}\n`)
      warned.set(id, why)
    }
  }
}
