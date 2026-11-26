import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, type Book, type BookSnap, type PMBookState, type PMEvent, type PMMarket, type PMState, type PricePoint } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { AccountPage, OrderTicket, SimPanel } from './PMSim'

// 预测市场（Polymarket）：系列（短期涨跌盘）概览、搜索、关注、事件详情（价格走势 + 订单簿）。

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

function Outcome({ m }: { m: PMMarket }) {
  if (m.winner >= 0) return <span className={m.winner === 0 ? 'tag tag-hot' : 'tag tag-cold'}>{m.outcomes[m.winner]}</span>
  if (m.closed) return <span className="tag tag-muted">待结算</span>
  return <span className="mono">{m.outcomes[0]} {cents(m.prices[0] ?? 0)}</span>
}

function BookLine({ b }: { b: PMBookState }) {
  if (!b.configured) return <p className="disclaimer" style={{ padding: '0 16px 12px' }}>盘口采集未启用（config.yaml 的 polymarket.book.every_ms）。</p>
  const s = b.status, x = b.stats
  return (
    <p className="disclaimer" style={{ padding: '0 16px 12px' }}>
      盘口采集：{s.running ? `运行中，每 ${s.every}` : '未运行'} · 目标 {s.targets.length} 个市场 · 本次启动写入 {s.books.toLocaleString()} 条
      · 本地共 {x.books.toLocaleString()} 条快照、{x.spots.toLocaleString()} 个现货价{x.first > 0 && <>（{when(x.first / 1000)} ~ {when(x.last / 1000)}）</>}
      {s.errors > 0 && <> · <span className="bad-text">连续失败 {s.errors} 次：{s.lastErr}</span></>}
    </p>
  )
}

function EventRow({ e }: { e: PMEvent }) {
  const m = e.markets[0]
  return (
    <tr onClick={() => { location.hash = `#/predict/${e.slug}` }}>
      <td className="mono nowrap">{m ? `${when(m.start)} → ${when(m.end).slice(6)}` : when(e.end)}</td>
      <td>{m ? <Outcome m={m} /> : '—'}</td>
      <td className="num mono">{usd(e.volume)}</td>
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
  const [series, setSeries] = useState('')
  const [upcoming, setUpcoming] = useState<PMEvent[]>([])
  const [recent, setRecent] = useState<PMEvent[]>([])
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<PMEvent[] | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(() => api.pm().then(setState, (e: Error) => setError(e.message)), [])
  useEffect(() => { load() }, [load])
  useEffect(() => { if (state?.configured && !series && state.series.length) setSeries(state.series[0]) }, [state, series])
  const running = state?.configured && state.status.running
  useEffect(() => {
    if (!running) return
    const id = window.setInterval(load, 3000)
    return () => window.clearInterval(id)
  }, [running, load])
  useEffect(() => {
    if (!series) return
    api.pmEvents(series, 'upcoming', 8).then(setUpcoming, (e: Error) => setError(e.message))
    api.pmEvents(series, 'closed', 40).then(setRecent, (e: Error) => setError(e.message))
  }, [series, state?.configured && state.status.lastRun])

  const search = () => {
    if (!q.trim()) return setHits(null)
    api.pmSearch(q.trim()).then(setHits, (e: Error) => setError(e.message))
  }

  if (state && !state.configured) {
    return (
      <div className="page">
        <div className="page-head"><div><h1>预测</h1></div></div>
        <div className="panel empty">未配置预测市场：在 config.yaml 的 polymarket 段填写 source 与 series 后重启。</div>
      </div>
    )
  }
  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>预测</h1>
          <p className="sub">Polymarket 公共行情（只读）。短期涨跌盘系列定时同步进本地（事件、结果、1 分钟价格）；其他事件可搜索、关注，关注后按小时同步价格。</p>
        </div>
      </div>
      {error && <div className="error" role="alert">{error}</div>}
      <section className="panel fund-search">
        <input placeholder="搜索 Polymarket 事件（英文关键词，如 bitcoin、fed、election），回车" value={q}
          onChange={(e) => setQ(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && search()} />
        {hits && (
          <ul className="fund-hits">
            {hits.map((e) => (
              <li key={e.id}>
                <span>{e.title}</span>
                <span className="muted small">24h {usd(e.volume24h)} · 截止 {when(e.end)}</span>
                <a className="btn btn-ghost" href={`#/predict/${e.slug}`}>查看</a>
              </li>
            ))}
            {hits.length === 0 && <li className="muted">没有找到进行中的事件。</li>}
          </ul>
        )}
      </section>
      {state?.configured && <SimPanel />}
      {state?.configured && state.watched.length > 0 && (
        <section className="panel">
          <div className="panel-head"><strong>关注</strong></div>
          <table className="table">
            <tbody>
              {state.watched.map((e) => (
                <tr key={e.slug} onClick={() => { location.hash = `#/predict/${e.slug}` }}>
                  <td>{e.title}</td>
                  <td className="muted nowrap">{e.closed ? '已结束' : `截止 ${when(e.end)}`}</td>
                  <td className="num mono">{e.volume ? usd(e.volume) : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
      {state?.configured && (
        <section className="panel">
          <div className="panel-head">
            <strong>系列</strong>
            <span className="muted">
              每 {state.status.every} 一轮{state.status.lastRun && <> · 上次 {new Date(state.status.lastRun).toLocaleString('zh-CN')}</>}
              {state.status.errors > 0 && <> · <span className="bad-text">{state.status.errors} 项失败</span></>}
            </span>
            <span style={{ flex: 1 }} />
            <button className="btn" disabled={state.status.running} onClick={() => api.syncPM().then(load, (e: Error) => setError(e.message))}>
              {state.status.running ? '同步中…' : '立即同步'}
            </button>
          </div>
          <div className="table-scroll">
            <table className="table">
              <thead><tr><th>系列</th><th className="num">事件</th><th className="num">已结算</th><th className="num">Up 胜率</th><th className="num">有价格历史</th><th>已结算覆盖</th></tr></thead>
              <tbody>
                {state.stats.map((s) => (
                  <tr key={s.series} className={s.series === series ? 'selected' : ''} onClick={() => setSeries(s.series)}>
                    <td className="mono">{s.series}</td>
                    <td className="num mono">{s.events.toLocaleString()}</td>
                    <td className="num mono">{s.closed.toLocaleString()}</td>
                    <td className="num mono">{s.resolved ? `${((s.firstWins / s.resolved) * 100).toFixed(1)}%` : '—'}</td>
                    <td className="num mono">{s.withHistory.toLocaleString()}</td>
                    <td className="mono small">{when(s.first)} ~ {when(s.last)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {state.status.log.length > 0 && <pre className="log" style={{ margin: '0 16px 16px' }}>{state.status.log.join('\n')}</pre>}
          <BookLine b={state.book} />
        </section>
      )}
      {series && (
        <div className="pf-two">
          <section className="panel">
            <div className="panel-head"><strong>最近结算</strong><span className="muted mono">{series}</span></div>
            <div className="scroll-y">
              <table className="table">
                <thead><tr><th>计价区间</th><th>结果</th><th className="num">成交额</th></tr></thead>
                <tbody>{recent.map((e) => <EventRow key={e.id} e={e} />)}</tbody>
              </table>
            </div>
          </section>
          <section className="panel">
            <div className="panel-head"><strong>进行中 / 即将开始</strong></div>
            <table className="table">
              <thead><tr><th>计价区间</th><th>Up 价</th><th className="num">成交额</th></tr></thead>
              <tbody>
                {upcoming.map((e) => <EventRow key={e.id} e={e} />)}
                {upcoming.length === 0 && <tr><td colSpan={3} className="muted">本地暂无，等下一轮同步。</td></tr>}
              </tbody>
            </table>
          </section>
        </div>
      )}
    </div>
  )
}

function EventDetail({ slug }: { slug: string }) {
  const [ev, setEv] = useState<PMEvent | null>(null)
  const [watched, setWatched] = useState(false)
  const [mi, setMi] = useState(0) // 选中的市场
  const [oi, setOi] = useState(0) // 选中的结果
  const [points, setPoints] = useState<PricePoint[]>([])
  const [priceSrc, setPriceSrc] = useState('')
  const [book, setBook] = useState<Book | null>(null)
  const [snaps, setSnaps] = useState<BookSnap[]>([]) // 本地采集的盘口快照（第一个结果代币）
  const [error, setError] = useState('')

  useEffect(() => {
    setEv(null); setMi(0); setOi(0); setError('')
    api.pmEvent(slug).then((r) => { setEv(r.event); setWatched(r.watched) }, (e: Error) => setError(e.message))
  }, [slug])
  const m: PMMarket | undefined = ev?.markets[mi]
  const token = m?.tokens[oi]

  // 价格走势：短期盘取计价区间前同样长的一段到结束（1 分钟）；其他取近 7 天
  useEffect(() => {
    if (!m || !token) return
    setPoints([])
    const start = secs(m.start), end = secs(m.end), span = end - start
    const short = span > 0 && span <= 86400 && start > 0
    const to = m.closed && end > 0 ? end : 0
    const req = short ? api.pmPrices(token, start - span, end, 1) : api.pmPrices(token, to ? to - 7 * 86400 : 0, to)
    req.then((r) => { setPoints(r.points); setPriceSrc(r.source) }, (e: Error) => setError(e.message))
  }, [m, token])

  // 订单簿：未结束的市场每 5 秒刷新
  useEffect(() => {
    setBook(null)
    if (!m || !token || m.closed) return
    const get = () => api.pmBook(token).then(setBook, () => setBook(null))
    get()
    const id = window.setInterval(get, 5000)
    return () => window.clearInterval(id)
  }, [m, token])

  // 本地盘口快照（短期盘）：未结束时每 10 秒刷新
  useEffect(() => {
    setSnaps([])
    if (!m || m.tokens.length === 0) return
    const start = secs(m.start), end = secs(m.end), span = end - start
    if (!(span > 0 && span <= 86400 && start > 0)) return
    const get = () => api.pmBooks(m.tokens[0], (start - span) * 1000, end * 1000).then(setSnaps, () => setSnaps([]))
    get()
    if (m.closed) return
    const id = window.setInterval(get, 10000)
    return () => window.clearInterval(id)
  }, [m])

  const option = useMemo<EChartsOption | null>(() => {
    if (points.length === 0 && snaps.length === 0) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line'), accent = cssVar('--accent')
    // 快照存的是第一个结果的盘口；看第二个结果时取镜像：买一 = 1 − 对方卖一，卖一 = 1 − 对方买一
    const side = (x: BookSnap, bid: boolean) => {
      const l = oi === 0 ? (bid ? x.bids[0] : x.asks[0]) : (bid ? x.asks[0] : x.bids[0])
      return l ? (oi === 0 ? l.price : 1 - l.price) : null
    }
    // 两次快照相隔超过 15 秒（采集中断）时断开，不画横跨空档的线
    const withGaps = (bid: boolean) => snaps.flatMap((x, i) =>
      i > 0 && x.t - snaps[i - 1].t > 15000 ? [[snaps[i - 1].t + 1, null], [x.t, side(x, bid)]] : [[x.t, side(x, bid)]])
    const bookSeries = snaps.length === 0 ? [] : ([['买一', true, '#d8453b'], ['卖一', false, '#1f8a4c']] as const).map(([name, bid, color]) => ({
      name, type: 'line' as const, step: 'end' as const, showSymbol: false, connectNulls: false,
      data: withGaps(bid), lineStyle: { width: 1, color }, itemStyle: { color },
    }))
    const marks = m && secs(m.start) > 0 && secs(m.end) - secs(m.start) <= 86400
      ? [{ xAxis: secs(m.start) * 1000, label: { formatter: '开始计价', color: ink } }] : []
    return {
      animation: false,
      grid: { left: 56, right: 24, top: 28, bottom: 56 },
      tooltip: { trigger: 'axis', valueFormatter: (v: unknown) => (typeof v === 'number' ? cents(v) : '—') },
      xAxis: { type: 'time', axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      yAxis: { type: 'value', min: (v: { min: number }) => Math.max(0, Math.floor((v.min - 0.02) * 50) / 50),
        max: (v: { max: number }) => Math.min(1, Math.ceil((v.max + 0.02) * 50) / 50), splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink, formatter: (v: number) => `${(v * 100).toFixed(0)}¢` } },
      dataZoom: [{ type: 'inside' }, { type: 'slider', height: 16, bottom: 8, borderColor: line, textStyle: { color: ink } }],
      series: [{
        name: m?.outcomes[oi] ?? '', type: 'line', step: 'end', showSymbol: false, data: points.map((p) => [p.t * 1000, p.p]),
        lineStyle: { width: 1.6, color: accent }, itemStyle: { color: accent },
        markLine: marks.length ? { symbol: 'none', lineStyle: { color: ink, type: 'dashed' }, data: marks } : undefined,
      }, ...bookSeries],
      legend: bookSeries.length ? { top: 0, left: 56, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 } : undefined,
    }
  }, [points, snaps, m, oi])

  const toggleWatch = () => {
    const p = watched ? api.pmUnwatch(slug) : api.pmWatch(slug)
    p.then(() => setWatched(!watched), (e: Error) => setError(e.message))
  }

  const bestBid = book?.bids[0]?.price, bestAsk = book?.asks[0]?.price
  // 互斥多结果：买齐所有 Yes 的成本（卖一之和）低于 1 即为套利（未计手续费与深度）；卖出所有 Yes 的所得（买一之和）高于 1 同理
  const open = ev?.markets.filter((x) => !x.closed) ?? []
  const sums = ev?.negRisk && open.length > 1
    ? { ask: open.reduce((a, x) => a + x.bestAsk, 0), bid: open.reduce((a, x) => a + x.bestBid, 0),
        askOk: open.every((x) => x.bestAsk > 0), bidOk: open.every((x) => x.bestBid > 0) } : null // 有市场缺卖一 / 买一时合计不可比
  return (
    <div className="page">
      <div className="page-head">
        <div>
          <a className="link" href="#/predict">← 预测</a>
          <h1>{ev?.title ?? slug}</h1>
          {ev && (
            <p className="sub">
              {ev.closed ? '已结束' : `截止 ${new Date(ev.end).toLocaleString('zh-CN')}`} · 成交额 {usd(ev.volume)} · 24h {usd(ev.volume24h)} · 流动性 {usd(ev.liquidity)}
              {ev.negRisk && ' · 互斥多结果'}{ev.series && <> · 系列 <span className="mono">{ev.series}</span></>}
              {' · '}<a className="link" href={`https://polymarket.com/event/${ev.slug}`} target="_blank" rel="noreferrer">在 Polymarket 打开</a>
            </p>
          )}
        </div>
        {ev && <button className="btn" onClick={toggleWatch}>{watched ? '取消关注' : '关注'}</button>}
      </div>
      {error && <div className="error" role="alert">{error}</div>}
      {ev && ev.markets.length > 1 && (
        <section className="panel">
          {sums && (
            <div className="panel-head">
              <strong>Yes 合计</strong>
              <span className="muted">
                卖一之和 {sums.askOk ? <span className={sums.ask < 1 ? 'warn-text' : 'mono'}>{cents(sums.ask)}</span> : '不全（有市场没有卖单）'}（买齐全部 Yes 的成本）·
                买一之和 {sums.bidOk ? <span className={sums.bid > 1 ? 'warn-text' : 'mono'}>{cents(sums.bid)}</span> : '不全（有市场没有买单）'}（卖出全部 Yes 的所得）·
                未计手续费与盘口深度，事件加载时的快照
              </span>
            </div>
          )}
          <div className="scroll-y">
            <table className="table">
              <thead><tr><th>市场</th><th className="num">{ev.markets[0]?.outcomes[0] ?? 'Yes'}</th><th className="num">买一 / 卖一</th><th className="num">成交额</th><th>状态</th></tr></thead>
              <tbody>
                {ev.markets.map((x, i) => (
                  <tr key={x.conditionId} className={i === mi ? 'selected' : ''} onClick={() => { setMi(i); setOi(0) }}>
                    <td>{x.groupTitle || x.question}</td>
                    <td className="num mono">{cents(x.prices[0] ?? 0)}</td>
                    <td className="num mono">{x.closed ? '—' : `${cents(x.bestBid)} / ${cents(x.bestAsk)}`}</td>
                    <td className="num mono">{usd(x.volume)}</td>
                    <td>{x.closed ? <Outcome m={x} /> : <span className="muted">{x.accepting ? '交易中' : '暂停'}</span>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
      {m && (
        <div className="pf-two">
          <section className="panel chart-panel">
            <div className="panel-head">
              <strong>{m.groupTitle || m.question}</strong>
              <div className="seg">
                {m.outcomes.map((o, i) => <button key={o} className={i === oi ? 'on' : ''} onClick={() => setOi(i)}>{o} {cents(m.prices[i] ?? 0)}</button>)}
              </div>
              <span className="muted small">{priceSrc === 'local' ? '本地数据' : priceSrc ? `实时（${priceSrc}）` : ''}</span>
            </div>
            {option ? <EChart option={option} height={360} /> : <div className="empty" style={{ height: 360 }}>暂无价格数据</div>}
            <p className="disclaimer" style={{ padding: '0 8px 12px' }}>
              计价 {when(m.start)} → {when(m.end)} · 最小单位 {m.tickSize} · 最小下单 {m.minSize}
              {m.feeRate ? <> · 吃单手续费 = 股数 × {m.feeRate} × p(1−p){m.feeExp && m.feeExp !== 1 ? `^${m.feeExp}` : ''}（50¢ 时约为成交额的 {(m.feeRate * 50).toFixed(1)}%）</>
                : m.feeType ? <> · 手续费 <span className="mono">{m.feeType}</span></> : null}
              {ev && ev.priceToBeat !== undefined && ev.priceToBeat > 0 && <> · 起始价 <span className="mono">{ev.priceToBeat.toFixed(2)}</span>（Chainlink）</>}
              {snaps.length > 0 && <> · 本地盘口快照 {snaps.length} 条</>}
              {m.winner >= 0 && <> · 结果 <strong>{m.outcomes[m.winner]}</strong></>}
            </p>
            {ev && <OrderTicket slug={ev.slug} m={m} outcome={oi} />}
          </section>
          <section className="panel">
            <div className="panel-head">
              <strong>订单簿</strong>
              <span className="muted small">{m.outcomes[oi]}{bestBid !== undefined && bestAsk !== undefined && <> · 价差 {cents(bestAsk - bestBid)}</>}</span>
            </div>
            {m.closed ? <div className="empty">市场已关闭</div> : !book ? <div className="empty">加载中…</div> : (
              <table className="table static">
                <thead><tr><th className="num">买价</th><th className="num">数量</th><th className="num">卖价</th><th className="num">数量</th></tr></thead>
                <tbody>
                  {Array.from({ length: Math.min(12, Math.max(book.bids.length, book.asks.length)) }, (_, i) => (
                    <tr key={i}>
                      <td className="num mono up">{book.bids[i] ? cents(book.bids[i].price) : ''}</td>
                      <td className="num mono">{book.bids[i]?.size.toFixed(0) ?? ''}</td>
                      <td className="num mono down">{book.asks[i] ? cents(book.asks[i].price) : ''}</td>
                      <td className="num mono">{book.asks[i]?.size.toFixed(0) ?? ''}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        </div>
      )}
    </div>
  )
}
