import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, ArrowLeft, ExternalLink, FlaskConical, Layers, Radio, RefreshCw, Scale, Search, Star, Wallet, X } from 'lucide-react'
import { api, type Book, type BookSnap, type PMAccountView, type PMEvent, type PMMarket, type PMState, type PricePoint } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { useTheme } from './theme'
import { AccountPage, OrderTicket, ShadowPanel, SimPanel } from './PMSim'
import { EvalPanel } from './PMEval'
import { ArbAndQueuePanel } from './PMArbQueue'
import { NotesBox } from './NotesBox'

const pad = (n: number) => String(n).padStart(2, '0')
function when(iso: string | number) {
  const d = typeof iso === 'number' ? new Date(iso * 1000) : new Date(iso)
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return '—'
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
const usd = (x: number) => (x >= 1e6 ? `$${(x / 1e6).toFixed(2)}M` : x >= 1e3 ? `$${(x / 1e3).toFixed(1)}K` : `$${x.toFixed(0)}`)
const cents = (p: number) => `${(p * 100).toFixed(1)}¢`
const secs = (iso: string) => Math.floor(new Date(iso).getTime() / 1000)
const slugFromHash = () => decodeURIComponent(location.hash.replace(/^#\/?predict\/?/, '').split('/')[0] ?? '')

type HomeTab = 'markets' | 'shadow' | 'eval' | 'arb' | 'sim'

function Outcome({ m }: { m: PMMarket }) {
  if (m.winner >= 0) return <span className={m.winner === 0 ? 'tag tag-hot' : 'tag tag-cold'}>{m.outcomes[m.winner]}</span>
  if (m.closed) return <span className="tag tag-muted">待结算</span>
  return (
    <span className="mono text-xs font-medium text-[var(--ink)]">
      {m.outcomes[0]} <span className="text-[var(--accent)]">{cents(m.prices[0] ?? 0)}</span>
    </span>
  )
}

function EventRow({ e }: { e: PMEvent }) {
  const m = e.markets[0]
  return (
    <tr
      onClick={() => {
        location.hash = `#/predict/${e.slug}`
      }}
    >
      <td className="mono nowrap text-xs">{m ? `${when(m.start)} → ${when(m.end).slice(6)}` : when(e.end)}</td>
      <td>{m ? <Outcome m={m} /> : '—'}</td>
      <td className="num mono text-xs text-[var(--ink-2)]">{usd(e.volume)}</td>
    </tr>
  )
}

export default function Predict() {
  const [slug, setSlug] = useState(slugFromHash)
  useEffect(() => {
    const on = () => setSlug(slugFromHash())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  const acct = /^#\/?predict\/acct\/(\d+)/.exec(location.hash)
  if (slug === 'acct' && acct) return <AccountPage id={Number(acct[1])} />
  return slug ? <EventDetail slug={slug} /> : <Home />
}

function Home() {
  const [state, setState] = useState<PMState | null>(null)
  const [simSummary, setSimSummary] = useState<{ accounts: PMAccountView[]; globalHalt: boolean } | null>(null)
  const [tab, setTab] = useState<HomeTab>('markets')
  const [series, setSeries] = useState('')
  const [upcoming, setUpcoming] = useState<PMEvent[]>([])
  const [recent, setRecent] = useState<PMEvent[]>([])
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<PMEvent[] | null>(null)
  const [error, setError] = useState('')
  const searchRef = useRef<HTMLDivElement>(null)

  const load = useCallback(() => {
    api.pm().then(setState, (e: Error) => setError(e.message))
    api.pmSim().then(setSimSummary, () => {})
  }, [])
  useEffect(() => {
    load()
  }, [load])
  useEffect(() => {
    if (state?.configured && !series && state.series.length) setSeries(state.series[0])
  }, [state, series])
  const running = state?.configured && state.status.running
  useEffect(() => {
    if (!running) return
    const id = window.setInterval(load, 3000)
    return () => window.clearInterval(id)
  }, [running, load])
  useEffect(() => {
    if (!series) return
    api.pmEvents(series, 'upcoming', 10).then(setUpcoming, (e: Error) => setError(e.message))
    api.pmEvents(series, 'closed', 40).then(setRecent, (e: Error) => setError(e.message))
  }, [series, state?.configured && state.status.lastRun])

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (searchRef.current && !searchRef.current.contains(e.target as Node)) setHits(null)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const search = () => {
    if (!q.trim()) return setHits(null)
    api.pmSearch(q.trim()).then(setHits, (e: Error) => setError(e.message))
  }

  if (state && !state.configured) {
    return (
      <div className="page">
        <div className="page-head">
          <div>
            <h1>Polymarket 预测市场</h1>
          </div>
        </div>
        <div className="panel empty">未配置预测市场：在 config.yaml 的 polymarket 段填写 source 与 series 后重启。</div>
      </div>
    )
  }

  const totalEvents = state?.configured ? state.stats.reduce((a, s) => a + s.events, 0) : 0
  const totalClosed = state?.configured ? state.stats.reduce((a, s) => a + s.closed, 0) : 0
  const simEquity = simSummary ? simSummary.accounts.reduce((a, x) => a + x.equity, 0) : 0
  const simCashInit = simSummary ? simSummary.accounts.reduce((a, x) => a + x.account.cash, 0) : 0
  const simPnl = simEquity - simCashInit
  const book = state?.configured ? state.book : null
  const shadow = state?.configured ? state.shadow : null
  const activeEvals = (shadow?.evals ?? []).filter((e) => e.ok).length

  return (
    <div className="page">
      {/* 顶部标题 + 悬浮搜索 + 同步按钮 */}
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="m-0">Polymarket 预测市场</h1>
          <p className="sub mt-1">短期涨跌盘行情、盘口快照采集、公允概率影子策略与二元结果模拟盘。</p>
        </div>

        <div className="flex flex-wrap items-center gap-2.5">
          <div ref={searchRef} className="relative w-64 sm:w-80">
            <Search className="pointer-events-none absolute top-1/2 left-3 h-3.5 w-3.5 -translate-y-1/2 text-[var(--ink-3)]" />
            <input
              placeholder="搜 Polymarket 事件（如 bitcoin、fed）回车…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') search()
                if (e.key === 'Escape') setHits(null)
              }}
              className="h-8.5 w-full rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] pr-8 pl-9 text-sm text-[var(--ink)] placeholder:text-[var(--ink-3)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[var(--accent-soft)] focus:outline-none"
            />
            {q && (
              <button
                type="button"
                onClick={() => {
                  setQ('')
                  setHits(null)
                }}
                className="absolute top-1/2 right-2.5 -translate-y-1/2 text-[var(--ink-3)] hover:text-[var(--ink)]"
                aria-label="清空"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
            {hits && (
              <ul className="absolute top-full right-0 left-0 z-30 mt-1.5 max-h-96 list-none overflow-y-auto rounded-xl border border-[var(--line-strong)] bg-[var(--panel)] p-1.5 shadow-[var(--shadow-md)]">
                {hits.map((e) => (
                  <li key={e.id}>
                    <a
                      href={`#/predict/${e.slug}`}
                      onClick={() => setHits(null)}
                      className="flex items-center justify-between gap-3 rounded-lg px-3 py-2 text-sm text-[var(--ink)] no-underline hover:bg-[var(--panel-2)]"
                    >
                      <span className="truncate font-medium">{e.title}</span>
                      <span className="mono shrink-0 text-xs text-[var(--ink-2)]">
                        24h {usd(e.volume24h)} · {when(e.end)}
                      </span>
                    </a>
                  </li>
                ))}
                {hits.length === 0 && <li className="py-6 text-center text-xs text-[var(--ink-2)]">没有找到进行中的事件</li>}
              </ul>
            )}
          </div>

          {state?.configured && (
            <button
              className="btn btn-primary"
              disabled={state.status.running}
              onClick={() => api.syncPM().then(load, (e: Error) => setError(e.message))}
            >
              <RefreshCw className={`h-3.5 w-3.5 ${state.status.running ? 'animate-spin' : ''}`} />
              {state.status.running ? '同步中…' : '立即同步'}
            </button>
          )}
        </div>
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      {/* 顶部 4 卡实时遥测条 */}
      {state?.configured && (
        <div className="grid grid-cols-2 gap-3.5 xl:grid-cols-4">
          <div className="panel p-4">
            <div className="flex items-center justify-between text-xs text-[var(--ink-2)]">
              <span>盘口采集</span>
              <span className="relative flex h-2 w-2">
                {book?.configured && book.status.running && (
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[var(--ok)] opacity-60" />
                )}
                <span
                  className={`relative inline-flex h-2 w-2 rounded-full ${
                    book?.configured && book.status.running ? 'bg-[var(--ok)]' : 'bg-[var(--ink-3)]'
                  }`}
                />
              </span>
            </div>
            <div className="mt-1.5 text-xl font-bold tracking-tight mono">
              {book?.configured ? `${book.stats.books.toLocaleString()}` : '未启用'}
              {book?.configured && <span className="ml-1 text-xs font-normal text-[var(--ink-2)]">快照</span>}
            </div>
            <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
              {book?.configured
                ? `每 ${book.status.every} · ${book.status.targets.length} 目标 · ${book.stats.spots.toLocaleString()} 现货${
                    book.status.wsEnabled ? ` · WS ${book.status.wsSpotOk && book.status.wsBookOk ? '已连' : '回退REST'}` : ''
                  }`
                : '配置 polymarket.book 启用'}
            </div>
          </div>

          <button
            type="button"
            onClick={() => setTab('shadow')}
            className="panel p-4 text-left transition-colors hover:border-[var(--line-strong)]"
          >
            <div className="flex items-center justify-between text-xs text-[var(--ink-2)]">
              <span>影子策略</span>
              <Radio className="h-3.5 w-3.5 text-[var(--accent)]" />
            </div>
            <div className="mt-1.5 text-xl font-bold tracking-tight mono">
              {shadow ? shadow.signals : 0}
              <span className="ml-1 text-xs font-normal text-[var(--ink-2)]">条信号</span>
            </div>
            <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
              {shadow ? `实时监控 ${activeEvals} 个盘口 · 阈值 ≥${(shadow.minEdge * 100).toFixed(0)}¢` : '未启用'}
            </div>
          </button>

          <button
            type="button"
            onClick={() => setTab('sim')}
            className="panel p-4 text-left transition-colors hover:border-[var(--line-strong)]"
          >
            <div className="flex items-center justify-between text-xs text-[var(--ink-2)]">
              <span>模拟盘权益</span>
              {simSummary?.globalHalt ? (
                <span className="tag tag-bad">熔断中</span>
              ) : (
                <Wallet className="h-3.5 w-3.5 text-[var(--ink-3)]" />
              )}
            </div>
            <div className="mt-1.5 flex items-baseline gap-2">
              <span className="text-xl font-bold tracking-tight mono">${simEquity.toFixed(2)}</span>
              {simSummary && simSummary.accounts.length > 0 && (
                <span className={`text-xs font-medium mono ${simPnl >= 0 ? 'up' : 'down'}`}>
                  {simPnl >= 0 ? '+' : '−'}${Math.abs(simPnl).toFixed(2)}
                </span>
              )}
            </div>
            <div className="mt-1 text-[11.5px] text-[var(--ink-3)]">
              {simSummary ? `${simSummary.accounts.length} 个模拟账户 · 点击管理` : '加载中…'}
            </div>
          </button>

          <div className="panel p-4">
            <div className="flex items-center justify-between text-xs text-[var(--ink-2)]">
              <span>系列同步</span>
              <span className={`tag ${state.status.running ? 'tag-cold' : 'tag-ok'}`}>
                {state.status.running ? '同步中' : `每 ${state.status.every}`}
              </span>
            </div>
            <div className="mt-1.5 text-xl font-bold tracking-tight mono">
              {totalEvents.toLocaleString()}
              <span className="ml-1 text-xs font-normal text-[var(--ink-2)]">事件</span>
            </div>
            <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
              已结算 {totalClosed.toLocaleString()} · {state.stats.length} 个系列
            </div>
          </div>
        </div>
      )}

      {/* 3 个清晰工作区 Tab */}
      <div className="tabs-bar" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={tab === 'markets'}
          className={`tab-btn ${tab === 'markets' ? 'on' : ''}`}
          onClick={() => setTab('markets')}
        >
          <Layers className="h-4 w-4" />
          市场与事件
          {state?.configured && <span className="tag tag-muted">{state.stats.length} 系列</span>}
        </button>
        {shadow && (
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'shadow'}
            className={`tab-btn ${tab === 'shadow' ? 'on' : ''}`}
            onClick={() => setTab('shadow')}
          >
            <Activity className="h-4 w-4" />
            影子策略监控
            <span className="tag tag-cold">{activeEvals} 进行中</span>
          </button>
        )}
        <button
          type="button"
          role="tab"
          aria-selected={tab === 'eval'}
          className={`tab-btn ${tab === 'eval' ? 'on' : ''}`}
          onClick={() => setTab('eval')}
        >
          <FlaskConical className="h-4 w-4" />
          信号评估与回放
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === 'arb'}
          className={`tab-btn ${tab === 'arb' ? 'on' : ''}`}
          onClick={() => setTab('arb')}
        >
          <Scale className="h-4 w-4" />
          套利与人工审批
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === 'sim'}
          className={`tab-btn ${tab === 'sim' ? 'on' : ''}`}
          onClick={() => setTab('sim')}
        >
          <Wallet className="h-4 w-4" />
          模拟盘与风控
          {simSummary && <span className="tag tag-muted">{simSummary.accounts.length}</span>}
        </button>
      </div>

      {/* Tab 1: 市场与事件 */}
      {state?.configured && tab === 'markets' && (
        <>
          {state.watched.length > 0 && (
            <section className="panel overflow-hidden">
              <div className="panel-head border-b border-[var(--line)]">
                <Star className="h-4 w-4 text-[var(--warn)]" />
                <strong>已关注事件</strong>
                <span className="tag tag-muted">{state.watched.length}</span>
              </div>
              <table className="table">
                <tbody>
                  {state.watched.map((e) => (
                    <tr
                      key={e.slug}
                      onClick={() => {
                        location.hash = `#/predict/${e.slug}`
                      }}
                    >
                      <td className="font-medium">{e.title}</td>
                      <td className="muted nowrap text-xs">{e.closed ? '已结束' : `截止 ${when(e.end)}`}</td>
                      <td className="num mono text-xs">{e.volume ? usd(e.volume) : ''}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>
          )}

          {/* 系列快速选择条 + 统计表 */}
          <section className="panel overflow-hidden">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3">
              <div className="flex flex-wrap items-center gap-2">
                <strong className="text-sm font-semibold">涨跌盘系列</strong>
                <div className="seg">
                  {state.stats.map((s) => (
                    <button key={s.series} className={s.series === series ? 'on' : ''} onClick={() => setSeries(s.series)}>
                      {s.series.replace('-up-or-down-', ' ').toUpperCase()}
                    </button>
                  ))}
                </div>
              </div>
              {state.status.lastRun && (
                <span className="mono text-xs text-[var(--ink-3)]">
                  上次同步 {new Date(state.status.lastRun).toLocaleString('zh-CN')}
                </span>
              )}
            </div>

            <div className="table-scroll">
              <table className="table">
                <thead>
                  <tr>
                    <th>系列</th>
                    <th className="num">事件</th>
                    <th className="num">已结算</th>
                    <th className="num">Up 胜率</th>
                    <th className="num">含价格历史</th>
                    <th>已结算覆盖</th>
                  </tr>
                </thead>
                <tbody>
                  {state.stats.map((s) => (
                    <tr key={s.series} className={s.series === series ? 'selected' : ''} onClick={() => setSeries(s.series)}>
                      <td className="mono font-medium text-[var(--accent)]">{s.series}</td>
                      <td className="num mono">{s.events.toLocaleString()}</td>
                      <td className="num mono">{s.closed.toLocaleString()}</td>
                      <td className="num mono">{s.resolved ? `${((s.firstWins / s.resolved) * 100).toFixed(1)}%` : '—'}</td>
                      <td className="num mono">{s.withHistory.toLocaleString()}</td>
                      <td className="mono small text-[var(--ink-2)]">
                        {when(s.first)} ~ {when(s.last)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {state.status.log.length > 0 && (
              <div className="border-t border-[var(--line)] p-4">
                <pre className="log">{state.status.log.join('\n')}</pre>
              </div>
            )}
          </section>

          {series && (
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 lg:items-start">
              <section className="panel overflow-hidden">
                <div className="panel-head border-b border-[var(--line)]">
                  <strong>进行中 / 即将开始</strong>
                  <span className="tag tag-ok">{upcoming.length}</span>
                  <span className="mono ml-auto text-xs text-[var(--ink-3)]">{series}</span>
                </div>
                <table className="table">
                  <thead>
                    <tr>
                      <th>计价区间</th>
                      <th>Up 价</th>
                      <th className="num">成交额</th>
                    </tr>
                  </thead>
                  <tbody>
                    {upcoming.map((e) => (
                      <EventRow key={e.id} e={e} />
                    ))}
                    {upcoming.length === 0 && (
                      <tr>
                        <td colSpan={3} className="muted">
                          本地暂无，等下一轮同步。
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </section>

              <section className="panel overflow-hidden">
                <div className="panel-head border-b border-[var(--line)]">
                  <strong>最近结算</strong>
                  <span className="tag tag-muted mono">{series}</span>
                </div>
                <div className="scroll-y">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>计价区间</th>
                        <th>结果</th>
                        <th className="num">成交额</th>
                      </tr>
                    </thead>
                    <tbody>
                      {recent.map((e) => (
                        <EventRow key={e.id} e={e} />
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>
            </div>
          )}
        </>
      )}

      {/* Tab 2: 影子策略 */}
      {state?.configured && tab === 'shadow' && shadow && <ShadowPanel s={shadow} />}

      {/* Tab 3: 信号评估与回放 */}
      {state?.configured && tab === 'eval' && <EvalPanel seriesList={state.series} />}

      {/* Tab 4: 套利与审批 */}
      {state?.configured && tab === 'arb' && <ArbAndQueuePanel accounts={simSummary?.accounts ?? []} />}

      {/* Tab 5: 模拟盘与风控 */}
      {state?.configured && tab === 'sim' && <SimPanel />}
    </div>
  )
}

function EventDetail({ slug }: { slug: string }) {
  const [ev, setEv] = useState<PMEvent | null>(null)
  const [watched, setWatched] = useState(false)
  const [mi, setMi] = useState(0)
  const [oi, setOi] = useState(0)
  const [points, setPoints] = useState<PricePoint[]>([])
  const [priceSrc, setPriceSrc] = useState('')
  const [book, setBook] = useState<Book | null>(null)
  const [snaps, setSnaps] = useState<BookSnap[]>([])
  const [error, setError] = useState('')
  const { theme } = useTheme()

  useEffect(() => {
    setEv(null)
    setMi(0)
    setOi(0)
    setError('')
    api.pmEvent(slug).then(
      (r) => {
        setEv(r.event)
        setWatched(r.watched)
      },
      (e: Error) => setError(e.message),
    )
  }, [slug])

  const m: PMMarket | undefined = ev?.markets[mi]
  const token = m?.tokens[oi]

  useEffect(() => {
    if (!m || !token) return
    setPoints([])
    const start = secs(m.start)
    const end = secs(m.end)
    const span = end - start
    const short = span > 0 && span <= 86400 && start > 0
    const to = m.closed && end > 0 ? end : 0
    const req = short
      ? api.pmPrices(token, start - span, end, 1)
      : api.pmPrices(token, to ? to - 7 * 86400 : 0, to)
    req.then(
      (r) => {
        setPoints(r.points)
        setPriceSrc(r.source)
      },
      (e: Error) => setError(e.message),
    )
  }, [m, token])

  useEffect(() => {
    setBook(null)
    if (!m || !token || m.closed) return
    const get = () => api.pmBook(token).then(setBook, () => setBook(null))
    get()
    const id = window.setInterval(get, 5000)
    return () => window.clearInterval(id)
  }, [m, token])

  useEffect(() => {
    setSnaps([])
    if (!m || m.tokens.length === 0) return
    const start = secs(m.start)
    const end = secs(m.end)
    const span = end - start
    if (!(span > 0 && span <= 86400 && start > 0)) return
    const get = () => api.pmBooks(m.tokens[0], (start - span) * 1000, end * 1000).then(setSnaps, () => setSnaps([]))
    get()
    if (m.closed) return
    const id = window.setInterval(get, 10000)
    return () => window.clearInterval(id)
  }, [m])

  const option = useMemo<EChartsOption | null>(() => {
    if (points.length === 0 && snaps.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const accent = cssVar('--accent')
    const side = (x: BookSnap, bid: boolean) => {
      const l = oi === 0 ? (bid ? x.bids[0] : x.asks[0]) : bid ? x.asks[0] : x.bids[0]
      return l ? (oi === 0 ? l.price : 1 - l.price) : null
    }
    const withGaps = (bid: boolean) =>
      snaps.flatMap((x, i) =>
        i > 0 && x.t - snaps[i - 1].t > 15000
          ? [
              [snaps[i - 1].t + 1, null],
              [x.t, side(x, bid)],
            ]
          : [[x.t, side(x, bid)]],
      )
    const bookSeries =
      snaps.length === 0
        ? []
        : (
            [
              ['买一', true, '#ef4444'],
              ['卖一', false, '#10b981'],
            ] as const
          ).map(([name, bid, color]) => ({
            name,
            type: 'line' as const,
            step: 'end' as const,
            showSymbol: false,
            connectNulls: false,
            data: withGaps(bid),
            lineStyle: { width: 1.2, color },
            itemStyle: { color },
          }))
    const marks =
      m && secs(m.start) > 0 && secs(m.end) - secs(m.start) <= 86400
        ? [{ xAxis: secs(m.start) * 1000, label: { formatter: '开始计价', color: ink } }]
        : []
    return {
      animation: false,
      grid: { left: 54, right: 24, top: 32, bottom: 56 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        valueFormatter: (v: unknown) => (typeof v === 'number' ? cents(v) : '—'),
      },
      xAxis: {
        type: 'time',
        axisLine: { lineStyle: { color: line } },
        axisLabel: { color: ink, fontSize: 11 },
      },
      yAxis: {
        type: 'value',
        min: (v: { min: number }) => Math.max(0, Math.floor((v.min - 0.02) * 50) / 50),
        max: (v: { max: number }) => Math.min(1, Math.ceil((v.max + 0.02) * 50) / 50),
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${(v * 100).toFixed(0)}¢` },
      },
      dataZoom: [
        { type: 'inside' },
        { type: 'slider', height: 18, bottom: 8, borderColor: cssVar('--line'), textStyle: { color: ink } },
      ],
      series: [
        {
          name: m?.outcomes[oi] ?? '',
          type: 'line',
          step: 'end',
          showSymbol: false,
          data: points.map((p) => [p.t * 1000, p.p]),
          lineStyle: { width: 2, color: accent },
          itemStyle: { color: accent },
          markLine: marks.length ? { symbol: 'none', lineStyle: { color: ink, type: 'dashed' }, data: marks } : undefined,
        },
        ...bookSeries,
      ],
      legend: bookSeries.length ? { top: 0, left: 54, textStyle: { color: ink, fontSize: 12 }, itemWidth: 14, itemHeight: 3 } : undefined,
    }
  }, [points, snaps, m, oi, theme])

  const toggleWatch = () => {
    const p = watched ? api.pmUnwatch(slug) : api.pmWatch(slug)
    p.then(() => setWatched(!watched), (e: Error) => setError(e.message))
  }

  const bestBid = book?.bids[0]?.price
  const bestAsk = book?.asks[0]?.price
  const open = ev?.markets.filter((x) => !x.closed) ?? []
  const sums =
    ev?.negRisk && open.length > 1
      ? {
          ask: open.reduce((a, x) => a + x.bestAsk, 0),
          bid: open.reduce((a, x) => a + x.bestBid, 0),
          askOk: open.every((x) => x.bestAsk > 0),
          bidOk: open.every((x) => x.bestBid > 0),
        }
      : null

  const bookRows = book ? Math.min(12, Math.max(book.bids.length, book.asks.length)) : 0
  const maxBookSize = book
    ? Math.max(1, ...book.bids.slice(0, 12).map((x) => x.size), ...book.asks.slice(0, 12).map((x) => x.size))
    : 1

  return (
    <div className="page">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <a
            href="#/predict"
            className="mb-1.5 inline-flex items-center gap-1 text-xs font-medium text-[var(--accent)] no-underline hover:underline"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            返回预测市场
          </a>
          <h1 className="m-0">{ev?.title ?? slug}</h1>
          {ev && (
            <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-[var(--ink-2)]">
              <span className={`tag ${ev.closed ? 'tag-muted' : 'tag-ok'}`}>
                {ev.closed ? '已结束' : `截止 ${new Date(ev.end).toLocaleString('zh-CN')}`}
              </span>
              <span className="mono">成交额 {usd(ev.volume)}</span>
              <span>·</span>
              <span className="mono">24h {usd(ev.volume24h)}</span>
              <span>·</span>
              <span className="mono">流动性 {usd(ev.liquidity)}</span>
              {ev.negRisk && <span className="tag tag-cold">互斥多结果</span>}
              {ev.series && <span className="tag tag-muted mono">{ev.series}</span>}
              <a
                className="inline-flex items-center gap-1 text-[var(--accent)] no-underline hover:underline"
                href={`https://polymarket.com/event/${ev.slug}`}
                target="_blank"
                rel="noreferrer"
              >
                Polymarket
                <ExternalLink className="h-3 w-3" />
              </a>
            </div>
          )}
        </div>
        {ev && (
          <button className={watched ? 'btn btn-primary' : 'btn'} onClick={toggleWatch}>
            <Star className={`h-3.5 w-3.5 ${watched ? 'fill-current' : ''}`} />
            {watched ? '已关注' : '关注'}
          </button>
        )}
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      {ev && ev.markets.length > 1 && (
        <section className="panel overflow-hidden">
          {sums && (
            <div className="flex flex-wrap items-center gap-2 border-b border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-3 text-xs">
              <strong className="text-[var(--ink)]">Yes 合计</strong>
              <span className="text-[var(--ink-2)]">
                卖一之和 {sums.askOk ? <span className={sums.ask < 1 ? 'warn-text' : 'mono text-[var(--ink)]'}>{cents(sums.ask)}</span> : '不全'}
                （买齐成本）· 买一之和{' '}
                {sums.bidOk ? <span className={sums.bid > 1 ? 'warn-text' : 'mono text-[var(--ink)]'}>{cents(sums.bid)}</span> : '不全'}
                （卖出所得）
              </span>
            </div>
          )}
          <div className="scroll-y">
            <table className="table">
              <thead>
                <tr>
                  <th>市场</th>
                  <th className="num">{ev.markets[0]?.outcomes[0] ?? 'Yes'}</th>
                  <th className="num">买一 / 卖一</th>
                  <th className="num">成交额</th>
                  <th>状态</th>
                </tr>
              </thead>
              <tbody>
                {ev.markets.map((x, i) => (
                  <tr
                    key={x.conditionId}
                    className={i === mi ? 'selected' : ''}
                    onClick={() => {
                      setMi(i)
                      setOi(0)
                    }}
                  >
                    <td className="font-medium">{x.groupTitle || x.question}</td>
                    <td className="num mono">{cents(x.prices[0] ?? 0)}</td>
                    <td className="num mono text-xs">
                      {x.closed ? '—' : `${cents(x.bestBid)} / ${cents(x.bestAsk)}`}
                    </td>
                    <td className="num mono text-xs">{usd(x.volume)}</td>
                    <td>{x.closed ? <Outcome m={x} /> : <span className="muted text-xs">{x.accepting ? '交易中' : '暂停'}</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {m && (
        <div className="grid grid-cols-1 gap-4 xl:grid-cols-[1fr_390px] xl:items-start">
          <section className="panel overflow-hidden">
            <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3.5">
              <div className="flex flex-wrap items-center gap-3">
                <strong className="text-sm font-semibold">{m.groupTitle || m.question}</strong>
                <div className="seg">
                  {m.outcomes.map((o, i) => (
                    <button key={o} className={i === oi ? 'on' : ''} onClick={() => setOi(i)}>
                      {o} {cents(m.prices[i] ?? 0)}
                    </button>
                  ))}
                </div>
              </div>
              {priceSrc && (
                <span className="tag tag-muted">{priceSrc === 'local' ? '本地数据' : `实时 (${priceSrc})`}</span>
              )}
            </div>

            <div className="p-3">
              {option ? <EChart option={option} height={360} /> : <div className="empty" style={{ height: 360 }}>暂无价格数据</div>}
            </div>

            <p className="disclaimer border-t border-[var(--line)] px-5 py-3">
              计价 {when(m.start)} → {when(m.end)} · 最小单位 {m.tickSize} · 最小下单 {m.minSize}
              {m.feeRate ? (
                <>
                  {' '}· 吃单手续费 = 股数 × {m.feeRate} × p(1−p)
                  {m.feeExp && m.feeExp !== 1 ? `^${m.feeExp}` : ''}（50¢ 约 {(m.feeRate * 50).toFixed(1)}%）
                </>
              ) : m.feeType ? (
                <> · 手续费 <span className="mono">{m.feeType}</span></>
              ) : null}
              {ev && ev.priceToBeat !== undefined && ev.priceToBeat > 0 && (
                <> · 起始价 <strong className="mono text-[var(--ink)]">{ev.priceToBeat.toFixed(2)}</strong>（Chainlink）</>
              )}
              {snaps.length > 0 && <> · 本地盘口快照 {snaps.length} 条</>}
              {m.winner >= 0 && <> · 结果 <strong className="text-[var(--ink)]">{m.outcomes[m.winner]}</strong></>}
            </p>
            {ev && <OrderTicket slug={ev.slug} m={m} outcome={oi} />}
          </section>

          <section className="panel overflow-hidden">
            <div className="flex items-center justify-between border-b border-[var(--line)] px-4 py-3.5">
              <strong className="text-sm font-semibold">实时订单簿 · {m.outcomes[oi]}</strong>
              {bestBid !== undefined && bestAsk !== undefined && (
                <span className="tag tag-muted mono">价差 {cents(bestAsk - bestBid)}</span>
              )}
            </div>
            {m.closed ? (
              <div className="empty">市场已关闭</div>
            ) : !book ? (
              <div className="empty">加载盘口中…</div>
            ) : (
              <div className="divide-y divide-[var(--line)] text-xs mono">
                <div className="grid grid-cols-4 bg-[var(--bg-subtle)] px-4 py-2 text-[11px] font-medium text-[var(--ink-3)]">
                  <span className="text-right">买价</span>
                  <span className="text-right">买量</span>
                  <span className="text-right">卖价</span>
                  <span className="text-right">卖量</span>
                </div>
                {Array.from({ length: bookRows }, (_, i) => {
                  const bid = book.bids[i]
                  const ask = book.asks[i]
                  const bidW = bid ? Math.min(100, (bid.size / maxBookSize) * 100) : 0
                  const askW = ask ? Math.min(100, (ask.size / maxBookSize) * 100) : 0
                  return (
                    <div key={i} className="grid grid-cols-2">
                      <div className="relative grid grid-cols-2 px-4 py-1.5">
                        <div
                          className="pointer-events-none absolute inset-y-0 right-0 bg-[#ef4444]/12"
                          style={{ width: `${bidW}%` }}
                        />
                        <span className="relative text-right font-medium up">{bid ? cents(bid.price) : ''}</span>
                        <span className="relative text-right text-[var(--ink-2)]">{bid ? bid.size.toFixed(0) : ''}</span>
                      </div>
                      <div className="relative grid grid-cols-2 border-l border-[var(--line)] px-4 py-1.5">
                        <div
                          className="pointer-events-none absolute inset-y-0 left-0 bg-[#10b981]/12"
                          style={{ width: `${askW}%` }}
                        />
                        <span className="relative text-right font-medium down">{ask ? cents(ask.price) : ''}</span>
                        <span className="relative text-right text-[var(--ink-2)]">{ask ? ask.size.toFixed(0) : ''}</span>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </section>
        </div>
      )}

      {ev && <NotesBox target={`pm:${ev.slug}`} label={ev.title} />}
    </div>
  )
}
