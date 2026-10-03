// The server: the local API every client talks to, the dashboard and the CLI alike. It is the one
// writer of the configuration file.
import { appendFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { readFile } from 'node:fs/promises'
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http'
import { basename, extname, isAbsolute, join, resolve, sep } from 'node:path'
import { address, defaults, readConfig, writeConfig } from './config.js'
import { board, type ProjectBoard } from './board.js'
import { abandon, abandonRequest, adopt, adoptRequest, claim, claimRequest, projectPath, resumable } from './claim.js'
import { check, decide, decideRequest, recheck } from './acceptance.js'
import { accept, merge, mergeRequest, release, releaseRequest, specRequest } from './actions.js'
import { notify } from './notify.js'
import { claimRuntime, type Quota, readQuota, runtimes, warnings } from './quota.js'
import { answers, type Answer, entries, type Entry } from './conversation.js'
import { recover } from './running.js'
import { type Announce, answer, begin, hold, type Runtime, say } from './session.js'
import { compactAt } from './settings.js'
import { finishHunt, hunt, resumableHunt } from './hunt.js'
import { capture, captureRequest, finish, plan, planRequest } from './plan.js'
import { apply, applyRequest, audit, auditAgain, finalize, finishStandardize, standardize } from './standardize.js'
import { open } from './terminal.js'
import { gate } from './gate.js'
import { resumeFix, review } from './review.js'
import { enter } from './engine.js'
import { graphOf } from './graphs.js'
import { followUps } from './ci.js'
import { checkout, derive, type Listed, type Project, Refusal } from './project.js'
import type { SessionRecord } from './records.js'
import { eventsFile, processId, readRecord, seen, watch } from './store.js'

export interface Options {
  // listen is the address the server listens on; the projects are read from the file on each request.
  listen: string
  configPath: string
  stateDir: string
  gh: string
  fake: boolean
  // runtime is what a session runs on: the claude executable and the directory of the bundled plugins.
  runtime: { claude: string; plugins: string }
  // dashboard is the directory of the dashboard's build, which the server serves at its root.
  dashboard: string
}

// quotaShare bounds how long a claim's answer waits for the quota after the claim is done, so a
// quota-axi that hangs on its provider delays the answer by no more than this.
const quotaShare = 2000

// within answers what p resolves to, or undefined once ms have passed without it.
function within<T>(p: Promise<T>, ms: number): Promise<T | undefined> {
  let timer: NodeJS.Timeout | undefined
  const late = new Promise<undefined>((resolve) => {
    timer = setTimeout(() => resolve(undefined), ms)
    timer.unref()
  })
  return Promise.race([p, late]).finally(() => clearTimeout(timer))
}

export function serve(o: Options): Server {
  mkdirSync(o.stateDir, { recursive: true })
  const log = (event: Record<string, unknown>) =>
    appendFileSync(join(o.stateDir, 'events.jsonl'), JSON.stringify({ at: new Date().toISOString(), ...event }) + '\n')

  // announce sends the notification of a process that turned blocked, ready or failed, unless the
  // configuration switches notifications off. It reads the file each time, as the list does, and a
  // notification that cannot be sent changes nothing of the process.
  const announce: Announce = (r) => {
    try {
      const c = readConfig(o.configPath)
      // An event log that cannot be written is told and still leaves the notification to be sent.
      try {
        log({ event: 'turned', process: r.id, state: r.state, notified: c.notifications })
      } catch (err) {
        process.stderr.write(`warning: ${r.id}: the turn to ${r.state} was not logged: ${(err as Error).message}\n`)
      }
      if (!c.notifications) return
      void notify(c.notifier, { title: `${basename(r.project)} ${r.issue === null ? r.branch : `#${r.issue}`} ${r.state}`, body: r.note })
    } catch (err) {
      process.stderr.write(`warning: ${r.id}: no notification of ${r.state} was sent: ${(err as Error).message}\n`)
    }
  }

  // The ci stage reads the pull request every 30 s, and the fake GitHub of fake mode at once.
  const rt: Runtime = { ...o.runtime, stateDir: o.stateDir, fake: o.fake, gh: o.gh, poll: o.fake ? 200 : 30000, announce }

  // The quota is read with the configured quota-axi on each request, so a change to the file shows at once.
  // Like readQuota it never rejects: a file that cannot be read makes every runtime read unknown.
  const quota = async (read: string[] = runtimes): Promise<Quota> => {
    try {
      const c = readConfig(o.configPath)
      return await readQuota(c.quota_axi, c.quota_minimum, read)
    } catch (err) {
      const reason = `the configuration cannot be read: ${(err as Error).message}`
      return { minimum: defaults.quota_minimum, runtimes: read.map((runtime) => ({ runtime, known: false, reason, below: false })) }
    }
  }

  // The list reads the file as add and remove do, so a project added or removed by hand shows at once.
  const list = (): Promise<Listed[]> =>
    Promise.all(
      readConfig(o.configPath).projects.map((path) =>
        derive(path, o.gh).catch((err: Error): Listed => ({ path, error: err.message })),
      ),
    )

  // The board is derived from the state directory, git and GitHub on every request and kept nowhere.
  // Without a project it is every project's; ?project=<path> asks for the project at that checkout.
  async function boards(res: ServerResponse, url: URL) {
    const wanted = url.searchParams.get('project')
    const paths = readConfig(o.configPath).projects
    if (wanted !== null) {
      // A path inside a checkout or a link to one asks for the project at its top, as remove does.
      const top = await checkout(resolve(wanted)).catch(() => resolve(wanted))
      const known = paths.includes(wanted) ? wanted : top
      if (!paths.includes(known)) return send(res, 404, { error: `${wanted} is not a project; ameise projects lists them` })
      const project = await derive(known, o.gh)
      return send(res, 200, await board(project, o.stateDir, o.gh))
    }
    const all = await Promise.all(
      paths.map((path) =>
        derive(path, o.gh)
          .then((p) => board(p, o.stateDir, o.gh))
          .catch((err: Error): ProjectBoard | { path: string; error: string } => ({ path, error: err.message })),
      ),
    )
    send(res, 200, { projects: all })
  }

  // known is the project a body names, by its checkout or a path inside it, as the board reads it.
  async function known(body: Record<string, unknown>): Promise<Project> {
    const wanted = projectPath(body)
    const paths = readConfig(o.configPath).projects
    const top = paths.includes(wanted) ? wanted : await checkout(wanted).catch(() => wanted)
    if (!paths.includes(top)) throw new Refusal(`${wanted} is not a project; ameise projects lists them`, 404)
    return derive(top, o.gh)
  }

  // A claim takes an issue into a work process; an abandon drops the process again.
  async function claimed(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const request = claimRequest(body)
    const project = await known(body)
    // The quota is read beside the claim and never holds it: the session starts once the claim is
    // done, and the answer waits for the reading no longer than the quota's share allows. Below the
    // minimum the claim goes on and its answer says so. It reads Claude alone, the one runtime it warns
    // of, so a slow reading of another runtime takes no warning away.
    const reading = quota([claimRuntime])
    const done = await claim(project, o.stateDir, o.gh, o.fake, request)
    log({ event: 'claimed', project: project.path, issue: request.issue, branch: done.record.branch, mode: request.mode, force: request.force })
    // The claimed process starts its implement session at once; the answer is its record as it runs.
    const record = begin(done.record, project, rt)
    const q = await within(reading, quotaShare)
    send(res, 201, { ...done, record, quota: q ? warnings(q) : [] })
  }

  async function abandoned(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const { issue, force } = abandonRequest(body)
    const project = await known(body)
    const done = await abandon(project, o.stateDir, issue, force, announce)
    log({ event: 'abandoned', project: project.path, issue, branch: done.branch, force })
    send(res, 200, done)
  }

  // A resume goes on with the session of an interrupted process in its worktree by its session id when
  // it has one, and starts a fresh session otherwise. A process interrupted in its gate command runs the
  // gate again, and one interrupted while its gate on CI waited takes its draft over and reads the head
  // again. One interrupted while its reviewers ran runs the round again, one interrupted in its pr
  // stage runs that stage again, one interrupted while its ci stage waited, or before its address-reviews
  // session started, waits again, and one interrupted in a fix session of its gate, its review or its
  // ci stage, or in its address-reviews session, goes on with that session. A fix
  // session of the review that had no id yet starts afresh with the findings of its round.
  // A hunt, which has no issue, is named by its id.
  async function resumed(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const byId = typeof body.id === 'string'
    const issue = byId ? null : abandonRequest(body).issue
    const project = await known(byId ? { project: recorded(body.id).project } : body)
    // The check and the start run in one go, so a second resume finds the process running.
    const interrupted = issue === null ? await resumableHunt(project, o.stateDir, body.id as string) : await resumable(project, o.stateDir, issue)
    const fix = interrupted.fixing === true && interrupted.session_id !== undefined
    // A fix session of the review that never reported its id starts afresh with the round's findings.
    const record =
      interrupted.stage === 'gate' && !fix
        ? gate(interrupted, project, rt)
        : interrupted.stage === 'review' && interrupted.fixing === true && !fix
          ? resumeFix(interrupted, project, rt)
          : interrupted.stage === 'review' && !fix
            ? review(interrupted, project, rt)
            : interrupted.stage === 'pr'
              ? enter(graphOf(interrupted), 'pr', interrupted, project, rt)
              : (interrupted.stage === 'ci' || interrupted.stage === 'address-reviews') && !fix
                ? enter(graphOf(interrupted), 'ci', interrupted, project, rt)
                : begin(interrupted, project, rt)
    log({ event: 'resumed', project: project.path, issue, branch: record.branch, session: record.session_id ?? null })
    send(res, 200, { record })
  }

  // An adopt takes a worktree this controller did not start into a process, which a resume starts.
  async function adopted(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const { issue, branch } = adoptRequest(body)
    const project = await known(body)
    const record = await adopt(project, o.stateDir, issue, branch)
    log({ event: 'adopted', project: project.path, issue, branch: record.branch })
    send(res, 201, { record })
  }

  // A merge takes a ready pull request into its base; a release tags a finished milestone; an acceptance
  // start opens a plan process on a spec whose tickets are all closed.
  async function merged(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const pr = mergeRequest(body)
    const project = await known(body)
    const done = await merge(project, o.stateDir, o.gh, o.fake, pr)
    log({ event: 'merged', project: project.path, pr, branch: done.branch, method: done.method, base: done.base })
    send(res, 200, done)
  }

  async function released(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const milestone = releaseRequest(body)
    const project = await known(body)
    const done = await release(project, o.stateDir, o.gh, o.fake, milestone)
    log({ event: done.status === 'released' ? 'released' : 'release waiting', project: project.path, milestone })
    send(res, done.status === 'released' ? 201 : 202, done)
  }

  async function accepted(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const spec = specRequest(body)
    const project = await known(body)
    const done = await accept(project, o.stateDir, o.gh, o.fake, spec)
    log({ event: 'accept', project: project.path, issue: spec, branch: done.branch })
    // The acceptance gathers its facts and runs its checker at once; the answer is its record as it runs.
    const record = check(done, project, rt)
    send(res, 201, { record })
  }

  // A check runs a failed acceptance again; the answers to its items write its gap tickets and
  // deviations, and close the spec once nothing is left open.
  async function checked(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const r = recorded(body.id)
    const project = await known({ project: r.project })
    const record = recheck(project, rt, r.id)
    log({ event: 'accept again', process: r.id, issue: r.issue })
    send(res, 200, { record })
  }

  async function decided(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const r = recorded(body.id)
    const answers = decideRequest(body)
    const project = await known({ project: r.project })
    const record = await decide(project, o.stateDir, o.gh, r.id, answers)
    log({ event: 'accept answered', process: r.id, issue: r.issue, gaps: record.acceptance?.gaps ?? [], closed: record.acceptance?.closed === true })
    send(res, 200, { record })
  }

  // A plan opens a plan process from an idea, an issue or nothing and starts its planner session at once;
  // the answer is its record as it runs.
  async function planned(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const request = planRequest(body)
    const project = await known(body)
    const done = await plan(project, o.stateDir, o.gh, o.fake, request)
    log({ event: 'planned', project: project.path, route: request.route, issue: done.issue, branch: done.branch })
    const record = begin(done, project, rt)
    send(res, 201, { record })
  }

  // A hunt opens a hunt process on a hunt branch and starts its hunt session at once; the answer is its
  // record as it runs, and what the hunt could not check.
  async function hunts(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const project = await known(body)
    const done = await hunt(project, o.stateDir, o.gh, o.fake)
    log({ event: 'hunted', project: project.path, branch: done.record.branch })
    const record = begin(done.record, project, rt)
    send(res, 201, { record, warnings: done.warnings })
  }

  // A standardize opens a standardize process on chore/standardize and starts its audit at once; the
  // answer is its record as it runs. Its audit runs again once it failed, its apply takes an answer per
  // category and applies the approved ones, and its finalize runs once the cleanup pull request is merged.
  async function standardized(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const project = await known(body)
    const done = await standardize(project, o.stateDir, o.gh, o.fake)
    log({ event: 'standardized', project: project.path, branch: done.branch })
    const record = audit(done, project, rt)
    send(res, 201, { record })
  }

  async function standardizeStep(req: IncomingMessage, res: ServerResponse, step: 'audit' | 'apply' | 'finalize') {
    const body = (await readJSON(req)) ?? {}
    const r = recorded(body.id)
    const answers = step === 'apply' ? applyRequest(body) : undefined
    const project = await known({ project: r.project })
    const record = step === 'audit' ? auditAgain(project, rt, r.id) : step === 'apply' ? apply(project, rt, r.id, answers) : finalize(project, rt, r.id)
    log({ event: `standardize ${step}`, process: r.id, ...(answers ? { answers } : {}) })
    send(res, 200, { record })
  }

  // A capture moves the prototype in a plan's worktree to a pushed prototype branch; a finish removes
  // the plan's worktree, branch and process.
  async function captured(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const record = recorded(body.id)
    const name = captureRequest(body)
    const project = await known({ project: record.project })
    const done = await capture(project, o.stateDir, o.fake, record.id, name)
    log({ event: 'captured', process: record.id, branch: done.branch })
    send(res, 201, { id: record.id, ...done })
  }

  // A finish of a hunt or a standardize process removes its worktree, branch and process the same way.
  async function finished(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const record = recorded(body.id)
    if (body.force !== undefined && typeof body.force !== 'boolean') throw new Refusal('force is not true or false')
    const project = await known({ project: record.project })
    const force = body.force === true
    const done =
      record.kind === 'hunt'
        ? await finishHunt(project, o.stateDir, record.id, force)
        : record.kind === 'standardize'
          ? await finishStandardize(project, o.stateDir, record.id, force)
          : await finish(project, o.stateDir, record.id, force)
    log({ event: 'finished', process: record.id, branch: done.branch, force: body.force === true })
    send(res, 200, { id: record.id, ...done })
  }

  // A process page that is opened marks its process seen, which clears its badge.
  async function opened(req: IncomingMessage, res: ServerResponse) {
    const id = ((await readJSON(req)) ?? {}).id
    if (typeof id !== 'string') return send(res, 400, { error: 'id is not the id of a process; send the id the board names' })
    if (!seen(o.stateDir, id)) return send(res, 404, { error: `${id} is not a process of this machine` })
    send(res, 200, { id })
  }

  // recorded is the record of the process a body or a query names, or a refusal.
  const recorded = (id: unknown): SessionRecord => {
    const r = readRecord(o.stateDir, processId(id))
    if (!r) throw new Refusal(`${String(id)} is not a process of this machine`, 404)
    return r
  }

  // A process page follows its process: the record and the conversation so far at once, then every
  // change as it is written, as server-sent events. A work process's record comes with the context size
  // at which its session compacts.
  function follow(res: ServerResponse, url: URL) {
    const record = recorded(url.searchParams.get('id'))
    // The log is read and the watch is set within one turn of the loop, so no line falls between them.
    // It is read before the stream starts, so a log that cannot be read is an error of the request.
    const file = eventsFile(o.stateDir, record.id)
    const lines = existsSync(file) ? readFileSync(file, 'utf8').split('\n').filter((l) => l !== '') : []
    res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache', [identity]: '1' })
    const out = (name: string, data: unknown) => res.write(`event: ${name}\ndata: ${JSON.stringify(data)}\n\n`)
    // Only a work session is pinned to compact at that size; a plan's compacts where its model does.
    const shown = (r: SessionRecord) => (r.kind === 'plan' ? r : { ...r, compact_at: compactAt })
    out('record', shown(record))
    let seq = 0
    const of = (e: Record<string, unknown>): Entry[] => entries(e, seq++, record.worktree)
    out(
      'entries',
      lines.flatMap((l) => {
        try {
          return of(JSON.parse(l) as Record<string, unknown>)
        } catch {
          seq++
          return []
        }
      }),
    )
    const unwatch = watch(record.id, (c) => {
      if ('gone' in c) {
        out('gone', {})
        res.end()
      } else if ('record' in c) out('record', shown(c.record))
      else {
        const list = of(c.event)
        if (list.length > 0) out('entries', list)
      }
    })
    // A comment now and then keeps a connection open through whatever would close an idle one.
    const ping = setInterval(() => res.write(': ping\n\n'), 20000)
    ping.unref()
    res.on('close', () => {
      unwatch()
      clearInterval(ping)
    })
  }

  // A message goes to the process's session: it answers the question that waits, is the next turn of
  // the session that runs, or resumes the session that has ended.
  async function message(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const record = recorded(body.id)
    const text = typeof body.text === 'string' ? body.text.trim() : ''
    if (text === '') throw new Refusal('text is empty; write the message to send')
    const how = await say(record, text, rt, () => known({ project: record.project }))
    send(res, 200, { id: record.id, delivered: how })
  }

  // A hold keeps the implement session open at its next complete report instead of starting the gate.
  async function held(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const record = recorded(body.id)
    if (typeof body.hold !== 'boolean') throw new Refusal('hold is not true or false')
    const done = hold(o.stateDir, record, body.hold)
    log({ event: body.hold ? 'held' : 'hold released', process: record.id })
    send(res, 200, { id: record.id, hold: done.hold === true })
  }

  // An answer settles a permission request of the process's session: once, for the process, or deny.
  async function answered(req: IncomingMessage, res: ServerResponse) {
    const body = (await readJSON(req)) ?? {}
    const record = recorded(body.id)
    if (typeof body.request !== 'string' || body.request === '') throw new Refusal('request is not the id of a request; send the id its card names')
    if (!answers.includes(body.answer as Answer)) throw new Refusal(`answer ${JSON.stringify(body.answer)} is none of ${answers.join(', ')}`)
    answer(record.id, body.request, body.answer as Answer)
    send(res, 200, { id: record.id, request: body.request, answer: body.answer })
  }

  // Open in terminal resumes the process's session by its id in a terminal window.
  async function terminal(req: IncomingMessage, res: ServerResponse) {
    const record = recorded(((await readJSON(req)) ?? {}).id)
    // The apply session of a standardize process runs once, within the steps of its apply.
    if (record.kind === 'standardize') throw new Refusal('the apply session of a standardize process runs once and resumes nowhere; apply again to start it afresh', 409)
    const script = await open(record, o.stateDir, readConfig(o.configPath).terminal, o.runtime)
    log({ event: 'terminal', process: record.id, session: record.session_id })
    send(res, 200, { id: record.id, script })
  }

  async function add(req: IncomingMessage, res: ServerResponse) {
    const path = await bodyPath(req, res)
    if (!path) return
    const project = await derive(resolve(path), o.gh)
    // The file is read again before each write, so a change made to it by hand while the server
    // runs is kept. The server takes over only the projects: it stays on the address it listens on.
    const config = readConfig(o.configPath)
    if (config.projects.includes(project.path)) return send(res, 409, { error: `${project.path} is already a project` })
    config.projects.push(project.path)
    writeConfig(o.configPath, config)
    send(res, 201, project)
  }

  async function remove(req: IncomingMessage, res: ServerResponse) {
    const path = await bodyPath(req, res)
    if (!path) return
    // A checkout names itself by its top, so a path inside one removes the project it belongs to.
    // The top is asked before the file is read, so no other change lands between the read and the
    // write.
    const absolute = resolve(path)
    const top = await checkout(absolute).catch(() => absolute)
    const config = readConfig(o.configPath)
    const known = config.projects.includes(absolute) ? absolute : top
    const i = config.projects.indexOf(known)
    if (i < 0) return send(res, 404, { error: `${absolute} is not a project; ameise projects lists them` })
    config.projects.splice(i, 1)
    writeConfig(o.configPath, config)
    send(res, 200, { path: known })
  }

  const server = createServer((req, res) => {
    // Node accepts a request target that URL refuses, such as //[, so a bad one is answered here
    // rather than thrown out of the server.
    let url: URL
    try {
      url = new URL(req.url ?? '/', 'http://localhost')
    } catch {
      return send(res, 400, { error: 'the request target is not a path' })
    }
    const route = `${req.method} ${url.pathname}`
    // A page on another site can make a browser send requests here. A Host that is not this server's
    // own name turns away a rebound DNS name. A write must say it is JSON, which a page can only do
    // after a preflight this server never grants.
    if (!loopbackHost(req.headers.host, o.listen)) return send(res, 403, { error: 'the Host header does not name this server' })
    if (req.method !== 'GET' && !(req.headers['content-type'] ?? '').startsWith('application/json')) {
      return send(res, 415, { error: 'a write is sent as application/json' })
    }
    const handle = async () => {
      switch (route) {
        case 'GET /api/projects':
          return send(res, 200, await list())
        case 'GET /api/board':
          return boards(res, url)
        case 'POST /api/projects':
          return add(req, res)
        case 'DELETE /api/projects':
          return remove(req, res)
        case 'POST /api/processes':
          return claimed(req, res)
        case 'DELETE /api/processes':
          return abandoned(req, res)
        case 'POST /api/processes/resume':
          return resumed(req, res)
        case 'POST /api/processes/adopt':
          return adopted(req, res)
        case 'POST /api/merges':
          return merged(req, res)
        case 'POST /api/releases':
          return released(req, res)
        case 'POST /api/acceptances':
          return accepted(req, res)
        case 'POST /api/acceptances/check':
          return checked(req, res)
        case 'POST /api/acceptances/answers':
          return decided(req, res)
        case 'POST /api/plans':
          return planned(req, res)
        case 'POST /api/hunts':
          return hunts(req, res)
        case 'POST /api/standardize':
          return standardized(req, res)
        case 'POST /api/standardize/audit':
          return standardizeStep(req, res, 'audit')
        case 'POST /api/standardize/apply':
          return standardizeStep(req, res, 'apply')
        case 'POST /api/standardize/finalize':
          return standardizeStep(req, res, 'finalize')
        case 'POST /api/processes/capture':
          return captured(req, res)
        case 'POST /api/processes/finish':
          return finished(req, res)
        case 'POST /api/processes/seen':
          return opened(req, res)
        case 'GET /api/processes/events':
          return follow(res, url)
        case 'POST /api/processes/message':
          return message(req, res)
        case 'POST /api/processes/answer':
          return answered(req, res)
        case 'POST /api/processes/hold':
          return held(req, res)
        case 'POST /api/processes/terminal':
          return terminal(req, res)
        case 'GET /api/quota':
          return send(res, 200, await quota())
        default:
          if (req.method === 'GET' && !url.pathname.startsWith('/api/')) return page(res, o.dashboard, url.pathname)
          return send(res, 404, { error: `no route ${route}` })
      }
    }
    handle().catch((err: Error) => {
      if (err instanceof TooLarge) send(res, 413, { error: err.message })
      else if (err instanceof Refusal) send(res, err.status, { ...err.more, error: err.message })
      else send(res, 500, { error: err.message })
    })
  })
  // The address the server listens on stays until it stops, whatever the file says meanwhile, so
  // the CLI reads it from the state directory rather than from the configuration.
  const record = join(o.stateDir, 'listen')
  // The follow-up reads the pull requests of the processes the ci stage left ready or blocked on a
  // review, four polls apart, one reading after the other (ci.ts).
  let followUp: NodeJS.Timeout | undefined
  const next = () => {
    followUp = setTimeout(() => {
      void followUps(rt, (path) => derive(path, o.gh)).finally(() => {
        if (followUp !== undefined) next()
      })
    }, rt.poll * 4)
    followUp.unref()
  }
  server.on('listening', () => {
    // The processes are read before the first request: a session the last run left running is gone.
    recover(o.stateDir)
    writeFileSync(record, o.listen + '\n')
    log({ event: 'started', fake: o.fake })
    next()
  })
  server.on('close', () => {
    clearTimeout(followUp)
    followUp = undefined
    rmSync(record, { force: true })
  })
  return server
}

function loopbackHost(host: string | undefined, listen: string): boolean {
  if (!host) return false
  const { port } = address(listen)
  const names = [`127.0.0.1:${port}`, `localhost:${port}`, `[::1]:${port}`, listen]
  // A client leaves the default port of http out of the Host it sends.
  if (port === 80) names.push('127.0.0.1', 'localhost', '[::1]', listen.replace(/:80$/, ''))
  return names.includes(host)
}

// identity is the header every answer carries, so the CLI tells this server from another service
// that took its port after it stopped.
export const identity = 'x-ameise'

// The kinds of file the dashboard's build holds. A file of another kind is not served.
const types: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
  '.woff': 'font/woff',
}

// The dashboard runs its own scripts alone and talks to this server alone, and no other site may frame
// it, so a page of another site cannot put the dashboard's buttons under its own. Styles may be inline,
// because a dialog locks the page's scroll with a style element it writes.
const policy = "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// page answers a file of the dashboard's build. The root is its index.html, which names every other
// file by a name that carries its content's hash; the page itself lives in the URL's fragment, so no
// other path is ever asked for. Without a build the root answers 404 with the command that makes one,
// and the API works as it does with one.
async function page(res: ServerResponse, dir: string, pathname: string) {
  const root = pathname === '/'
  const file = buildFile(dir, pathname)
  const type = file && types[extname(file)]
  if (!file || !type) return send(res, 404, { error: `no route GET ${pathname}` })
  let body: Buffer
  try {
    body = await readFile(file)
  } catch {
    if (root) return send(res, 404, { error: 'the dashboard is not built; run npm --prefix dashboard run build' })
    return send(res, 404, { error: `no route GET ${pathname}` })
  }
  res.writeHead(200, {
    'content-type': type,
    // index.html names the build, so it is asked for again each time, at / and at its own name; a file
    // under assets carries its content's hash and never changes under its name. The icons beside the
    // index keep their names across builds, so they are asked for again too.
    'cache-control': pathname.startsWith('/assets/') ? 'public, max-age=31536000, immutable' : 'no-cache',
    'content-security-policy': policy,
    'x-content-type-options': 'nosniff',
    [identity]: '1',
  })
  res.end(body)
}

// buildFile is the file of the build at dir that a path names, or undefined when the path leaves the
// build or names a kind of file the build does not hold. The URL parser drops the dot segments of a
// request target before the path gets here, and this check holds should that ever change.
export function buildFile(dir: string, pathname: string): string | undefined {
  const file = resolve(dir, pathname === '/' ? 'index.html' : '.' + pathname)
  return file.startsWith(dir + sep) && types[extname(file)] ? file : undefined
}

// send answers the request with the body, or ends a response that has already started, such as a stream.
function send(res: ServerResponse, status: number, body: unknown) {
  if (res.headersSent) return void res.end()
  res.writeHead(status, { 'content-type': 'application/json', [identity]: '1' })
  res.end(JSON.stringify(body) + '\n')
}

// bodyPath is the absolute path the body names, or undefined once the refusal is sent.
async function bodyPath(req: IncomingMessage, res: ServerResponse): Promise<string | undefined> {
  const body = await readJSON(req)
  const path = typeof body?.path === 'string' ? body.path : ''
  if (path && isAbsolute(path)) return path
  send(res, 400, { error: 'path is not an absolute path; name the checkout as an absolute path' })
  return undefined
}

// A body is a path or a claim and nothing more, so one past this size is refused before it fills the memory.
const bodyLimit = 64 * 1024

class TooLarge extends Error {}

async function readJSON(req: IncomingMessage): Promise<Record<string, unknown> | undefined> {
  const raw = await new Promise<string>((resolve, reject) => {
    let body = ''
    req.setEncoding('utf8')
    const take = (chunk: string) => {
      body += chunk
      if (body.length <= bodyLimit) return
      // The rest of the body is read and dropped, so the answer still reaches the client.
      req.off('data', take)
      req.resume()
      reject(new TooLarge(`the body is larger than ${bodyLimit} bytes`))
    }
    req.on('data', take)
    req.on('end', () => resolve(body))
    req.on('error', reject)
  })
  if (!raw) return undefined
  try {
    return JSON.parse(raw) as Record<string, unknown>
  } catch {
    throw new Refusal('the body is not JSON; send a JSON object')
  }
}
