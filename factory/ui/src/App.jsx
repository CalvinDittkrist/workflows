import { useEffect, useRef, useState } from 'react'

// The dashboard reads the factory's read-only interface and never writes: it polls /api/status,
// /api/repositories and /api/line for the three areas, and /api/runs/{id} for the selected run.

// The stages of the worker pipeline in the order a run moves through them. The factory reads the
// stage a run is in from the worker's skill calls; this is the line those stages are shown on.
const STAGES = ['implement', 'review', 'pr', 'ci', 'reviews']

// Of the runs that are done, the newest are drawn: the factory keeps every run it ever made, and a
// page that drew them all would grow with the months. The older ones are reached by their id.
const SHOWN = 100

const SLOW = 2000 // the three areas
const FAST = 1000 // the selected run, whose log is followed while it is written

// Every read goes through here. A failure carries the status, so a reader of it can tell a run that
// is not on this factory from a factory that stopped answering.
const get = (url) =>
  fetch(url).then((r) => {
    if (r.ok) return r.json()
    const failed = new Error(`${url}: ${r.status}`)
    failed.status = r.status
    throw failed
  })

// A run's colour and word come from its outcome once it has ended, and from the fact that it is
// still going while it has not.
const stateOf = (run) => (run.endedAt ? run.outcome : 'running')

// now ticks once a second, so every duration on the page counts up by itself.
function useNow() {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [])
  return now
}

function duration(from, to) {
  const s = Math.max(0, Math.round((new Date(to) - new Date(from)) / 1000))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (h) return `${h}h ${String(m).padStart(2, '0')}m`
  return m ? `${m}m ${String(s % 60).padStart(2, '0')}s` : `${s}s`
}

const tokens = (n) => (n >= 1000 ? `${(n / 1000).toFixed(1)}k` : String(n || 0))
const clock = (at) => new Date(at).toLocaleTimeString('en-GB')

// The selected run lives in the URL (#run=2), so a run can be linked, survives a reload and is
// reached by editing the address.
const runInHash = () => Number(new URLSearchParams(location.hash.slice(1)).get('run')) || null

// A duration or a clock time, the one thing on the page that differs between two readings of the
// same state. It is marked so a screenshot can hold everything around it.
function Tick({ children }) {
  return <span className="tick">{children}</span>
}

// Facts are a row of short ones, separated the way the terminal separates them.
function Facts({ className = 'facts', items }) {
  const shown = items.filter(Boolean)
  return (
    <p className={className}>
      {shown.map((item, i) => (
        <span key={i}>
          {i > 0 ? ' · ' : ''}
          {item}
        </span>
      ))}
    </p>
  )
}

function State({ state }) {
  return (
    <span className={`state state-${state}`}>
      <i />
      {state}
    </span>
  )
}

// A tool event's title is "<Tool> <label>"; the tool's name reads as a name only when it is set apart.
function What({ event }) {
  if (event.kind !== 'tool') return event.title
  const [name, ...label] = event.title.split(' ')
  return (
    <>
      <b>{name}</b>
      {label.join(' ')}
    </>
  )
}

export default function App() {
  const [status, setStatus] = useState(null)
  const [repositories, setRepositories] = useState([])
  const [line, setLine] = useState(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(runInHash)
  const now = useNow()

  useEffect(() => {
    let stop = false
    const load = () =>
      Promise.all([get('/api/status'), get('/api/repositories'), get('/api/line')])
        .then(([nextStatus, nextRepositories, nextLine]) => {
          if (stop) return
          setStatus(nextStatus)
          setRepositories(nextRepositories)
          setLine(nextLine)
          setError('')
        })
        .catch((e) => {
          if (!stop) setError(e.message)
        })
    load()
    const t = setInterval(load, SLOW)
    return () => {
      stop = true
      clearInterval(t)
    }
  }, [])

  useEffect(() => {
    const follow = () => setSelected(runInHash())
    addEventListener('hashchange', follow)
    return () => removeEventListener('hashchange', follow)
  }, [])

  const select = (id) => {
    history.replaceState(null, '', `#run=${id}`)
    setSelected(id)
  }

  if (!status || !line) return <div className="boot">{error || 'connecting…'}</div>

  const done = [...line.done].reverse() // newest first: the last run is the one being looked for
  const shown = done.slice(0, SHOWN)
  const active = line.now[0] ?? null
  // Follow the factory until the reader picks a run.
  const current = selected ?? active?.id ?? done[0]?.id ?? null
  const mode = status.state === 'running' ? (active ? 'working' : 'idle') : status.state

  return (
    <div className="app">
      <header className="top">
        <b>factory</b>
        <span className="where">
          up <Tick>{duration(status.startedAt, now)}</Tick>
        </span>
        {status.fake && <span className="scripted">scripted</span>}
        <span className={`mode mode-${mode}`}>
          <i />
          {mode.replace(/-/g, ' ')}
          {status.quotaUntil && <> · until {clock(status.quotaUntil)}</>}
        </span>
      </header>
      {error && <div className="banner">{error}</div>}

      <aside className="pane repos">
        <h2>Repositories</h2>
        <ul>
          {repositories.map((r) => (
            <li key={r.repository}>
              <i />
              <span>{r.repository}</span>
              <b>{r.queued}</b>
            </li>
          ))}
        </ul>
      </aside>

      <section className="pane line">
        <h2>Now</h2>
        {active ? (
          <button
            type="button"
            className={`row now${active.id === current ? ' on' : ''}`}
            onClick={() => select(active.id)}
          >
            <span className="pos">
              <i />
            </span>
            <span className="title">
              <em>#{active.issue}</em>
              {active.title}
            </span>
            <Facts items={[active.repository, active.stage, <Tick key="t">{duration(active.startedAt, now)}</Tick>]} />
          </button>
        ) : (
          <p className="none">{mode.replace(/-/g, ' ')}</p>
        )}

        <h2>
          Queue<b>{line.queue.length}</b>
        </h2>
        {line.queue.length === 0 && <p className="none">empty</p>}
        <ol>
          {line.queue.map((issue, k) => (
            <li key={`${issue.repository}#${issue.number}`} className="row">
              <span className="pos">{k + 1}</span>
              <span className="title">
                <em>#{issue.number}</em>
                {issue.title}
              </span>
              <Facts items={[issue.repository]} />
            </li>
          ))}
        </ol>

        <h2>
          Done<b>{done.length}</b>
        </h2>
        {done.length === 0 && <p className="none">nothing yet</p>}
        {shown.map((run) => (
          <button
            type="button"
            key={run.id}
            className={`row state-${stateOf(run)}${run.id === current ? ' on' : ''}`}
            onClick={() => select(run.id)}
          >
            <span className="pos">
              <i />
            </span>
            <span className="title">
              <em>#{run.issue}</em>
              {run.title}
            </span>
            <Facts
              items={[
                <span key="o" className="outcome-word">
                  {run.outcome}
                </span>,
                run.repository,
                <Tick key="t">{duration(run.startedAt, run.endedAt)}</Tick>,
                run.costUsd ? `$${run.costUsd.toFixed(2)}` : '',
              ]}
            />
          </button>
        ))}
        {done.length > shown.length && <p className="none">{done.length - shown.length} older</p>}
      </section>

      {current ? (
        <Run key={current} id={current} now={now} />
      ) : (
        <section className="pane detail">
          <p className="none">no run yet</p>
        </section>
      )}
    </div>
  )
}

// Run is the selected run: what it is, how far it got, how it ended, and its log as it is written.
function Run({ id, now }) {
  const [run, setRun] = useState(null)
  const [missing, setMissing] = useState(false)
  const [trouble, setTrouble] = useState('')
  const [events, setEvents] = useState([])
  const [open, setOpen] = useState({})
  const seen = useRef(0)
  const log = useRef(null)
  const pinned = useRef(true)

  useEffect(() => {
    let stop = false
    let follow = null
    seen.current = 0
    const load = () =>
      get(`/api/runs/${id}?after=${seen.current}`)
        .then((next) => {
          if (stop) return
          if (next.events?.length) {
            seen.current = next.events[next.events.length - 1].seq
            // The reader asks only for what it has not seen; a repeated answer is dropped all the
            // same, so a second reading of the same events cannot double the log.
            setEvents((old) => {
              const last = old.length ? old[old.length - 1].seq : 0
              return [...old, ...next.events.filter((e) => e.seq > last)]
            })
          }
          setRun(next)
          setMissing(false)
          setTrouble('')
          // A run that has ended is written once and never again: this answer carries its record and
          // the rest of its log, so a dashboard left open stops asking for it.
          if (next.endedAt && follow) {
            clearInterval(follow)
            follow = null
          }
        })
        .catch((e) => {
          if (stop) return
          // A run this factory does not have is not going to appear later, so it is asked for once.
          if (e.status === 404) {
            setMissing(true)
            if (follow) {
              clearInterval(follow)
              follow = null
            }
          } else setTrouble(e.message)
        })
    load()
    follow = setInterval(load, FAST)
    return () => {
      stop = true
      if (follow) clearInterval(follow)
    }
  }, [id])

  // The log follows the worker as long as the reader has not scrolled away from its end.
  useEffect(() => {
    if (pinned.current && log.current) log.current.scrollTop = log.current.scrollHeight
  }, [events])

  if (missing) {
    return (
      <section className="pane detail">
        <p className="none">run {id} is not on this factory</p>
      </section>
    )
  }
  if (!run) return <section className="pane detail" />

  const state = stateOf(run)
  const reached = new Set(run.stages)
  const versions = [
    ['worker', run.versions.worker],
    ['claude code', run.versions.claudeCode],
    ['factory', run.versions.factory],
  ].filter(([, version]) => version)

  return (
    <section className="pane detail">
      <div className="head">
        <h3>
          <em>#{run.issue}</em>
          {run.title}
        </h3>
        <State state={state} />
      </div>
      {trouble && <p className="trouble">{trouble}</p>}
      <Facts
        items={[
          run.repository,
          run.model,
          <Tick key="t">{duration(run.startedAt, run.endedAt ?? now)}</Tick>,
          run.turns ? `${run.turns} turns` : '',
          run.contextPeak ? `${tokens(run.contextPeak)} context peak` : '',
          run.costUsd ? `$${run.costUsd.toFixed(2)}` : '',
        ]}
      />
      {versions.length > 0 && (
        <Facts className="facts versions" items={versions.map(([name, version]) => `${name} ${version}`)} />
      )}
      <ol className="steps">
        {STAGES.map((stage) => (
          <li key={stage} className={stage === run.stage ? `at state-${state}` : reached.has(stage) ? 'done' : ''}>
            {stage}
          </li>
        ))}
      </ol>
      {run.warnings?.length > 0 && (
        <ul className="warnings">
          {run.warnings.map((warning, i) => (
            <li key={i}>{warning}</li>
          ))}
        </ul>
      )}
      {run.endedAt && (
        <div className={`outcome state-${state}`}>
          <b>{run.outcome}</b>
          {run.pullRequest && (
            <a href={run.pullRequest} target="_blank" rel="noreferrer">
              {run.pullRequest}
            </a>
          )}
          {run.reason && <p>{run.reason}</p>}
        </div>
      )}
      <div
        className="log"
        ref={log}
        onScroll={(e) => {
          const el = e.currentTarget
          pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
        }}
      >
        {events.map((e) => (
          <div key={e.seq} className={`ev ev-${e.kind}${e.sub ? ' ev-sub' : ''}${open[e.seq] ? ' ev-open' : ''}`}>
            <button type="button" disabled={!e.body} onClick={() => setOpen((o) => ({ ...o, [e.seq]: !o[e.seq] }))}>
              <time className="tick">{clock(e.at)}</time>
              <span className="kind">{e.kind}</span>
              <span className="what">
                <What event={e} />
              </span>
            </button>
            {open[e.seq] && <pre>{e.body}</pre>}
          </div>
        ))}
      </div>
    </section>
  )
}
