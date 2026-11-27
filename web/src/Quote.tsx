import { Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { ArrowDownRight, ArrowUpRight, CandlestickChart, Search, X } from 'lucide-react'
import { api, type SecurityHit, type SecuritySeries } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { pct } from './temperature'
import { useTheme } from './theme'
import { RENDERERS, useKlineRenderer, type KlineData } from './kline/kline'
import { NotesBox } from './NotesBox'

const KIND_TEXT: Record<string, string> = { stock: '股票', fund: '场内基金', ofund: '场外基金' }
const MAS = [20, 60, 250]

function sma(xs: number[], n: number): (number | null)[] {
  const out: (number | null)[] = []
  let s = 0
  xs.forEach((x, i) => {
    s += x
    if (i >= n) s -= xs[i - n]
    out.push(i >= n - 1 ? s / n : null)
  })
  return out
}

/** 最新值在自身历史（非空、正数）中的分位，0–100。 */
function percentile(xs: (number | null)[]): { last: number; pct: number; n: number; from: number } | null {
  const idx = xs.map((x, i) => (x !== null && x > 0 ? i : -1)).filter((i) => i >= 0)
  if (idx.length < 20) return null
  const vals = idx.map((i) => xs[i] as number)
  const last = vals[vals.length - 1]
  const below = vals.filter((v) => v < last).length
  const equal = vals.filter((v) => v === last).length
  return { last, pct: ((below + 0.5 * equal) / vals.length) * 100, n: vals.length, from: idx[0] }
}

/** 当前地址中的证券：#/quote/{kind}/{code} */
function fromHash(): { kind: string; code: string } | null {
  const m = location.hash.match(/^#\/quote\/(stock|fund|ofund)\/(\d{6})/)
  return m ? { kind: m[1], code: m[2] } : null
}

type FillItem = {
  id: number; accountId: number; accountName: string; side: 'buy' | 'sell'; fillDate: string; fillPrice: number; qty: number; amount: number; note?: string
}

export default function Quote() {
  const [target, setTarget] = useState(fromHash)
  const [data, setData] = useState<SecuritySeries | null>(null)
  const [fills, setFills] = useState<FillItem[]>([])
  const [mode, setMode] = useState<'qfq' | 'hfq' | 'none'>('qfq')
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<SecurityHit[]>([])
  const [openHits, setOpenHits] = useState(false)
  const [error, setError] = useState('')
  const searchRef = useRef<HTMLDivElement>(null)
  const { theme } = useTheme()

  useEffect(() => {
    const on = () => setTarget(fromHash())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (searchRef.current && !searchRef.current.contains(e.target as Node)) setOpenHits(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  useEffect(() => {
    if (!target) return
    setError('')
    setFills([])
    api.securitySeries(target.kind, target.code).then(setData, (e) => {
      setData(null)
      setError(e.message)
    })
    if (target.kind === 'stock' || target.kind === 'fund') {
      api.securityFills(target.kind, target.code).then(setFills, () => {})
    }
  }, [target])

  useEffect(() => {
    if (!q.trim()) {
      setHits([])
      setOpenHits(false)
      return
    }
    const t = setTimeout(
      () =>
        api.searchSecurities(q).then(
          (r) => {
            setHits(r.items)
            setOpenHits(true)
          },
          () => {},
        ),
      200,
    )
    return () => clearTimeout(t)
  }, [q])

  const prices = useMemo(() => {
    if (!data) return []
    if (data.kind === 'ofund') return mode === 'none' ? data.close : data.adjusted
    if (mode === 'none') return data.close
    if (mode === 'hfq') return data.adjusted
    const n = data.adjusted.length
    const k = n > 0 && data.adjusted[n - 1] > 0 ? data.close[n - 1] / data.adjusted[n - 1] : 1
    return data.adjusted.map((x) => x * k)
  }, [data, mode])

  const modeText =
    data?.kind === 'ofund'
      ? mode === 'none'
        ? '单位净值'
        : '累计净值'
      : mode === 'qfq'
        ? '前复权'
        : mode === 'hfq'
          ? '后复权'
          : '不复权'
  const hasK = !!data?.high

  const kline = useMemo<KlineData | null>(() => {
    if (!data || data.dates.length === 0) return null
    const colors = [cssVar('--t-hot-1'), cssVar('--t-cold-2'), cssVar('--dim-2')]
    const scaled = (xs: (number | null)[] | undefined) =>
      hasK && xs ? xs.map((x, i) => (x === null ? null : (x * prices[i]) / data.close[i])) : null
    return {
      dates: data.dates,
      close: prices,
      open: scaled(data.open),
      high: scaled(data.high),
      low: scaled(data.low),
      volume: hasK ? (data.volume ?? null) : null,
      lines: data.kind === 'ofund' ? [] : MAS.map((n, i) => ({ name: `MA${n}`, color: colors[i], data: sma(prices, n) })),
      title: hasK ? `K 线（${modeText}）` : data.kind === 'ofund' ? modeText : `收盘（${modeText}）`,
    }
  }, [data, prices, modeText, hasK, theme])

  const [renderer, setRenderer] = useKlineRenderer()

  const valuation = useMemo(() => {
    if (!data?.peTtm || !data.pb) return null
    const pe = percentile(data.peTtm)
    const pb = percentile(data.pb)
    if (!pe && !pb) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const from = Math.min(pe?.from ?? Infinity, pb?.from ?? Infinity)
    const dates = data.dates.slice(from)
    const option: EChartsOption = {
      animation: false,
      tooltip: {
        trigger: 'axis',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(2) : '—'),
      },
      legend: { top: 0, left: 0, textStyle: { color: ink, fontSize: 12 }, itemWidth: 14, itemHeight: 3 },
      grid: { left: 52, right: 52, top: 34, bottom: 50 },
      xAxis: {
        type: 'category',
        data: dates,
        boundaryGap: false,
        axisLine: { lineStyle: { color: line } },
        axisTick: { show: false },
        axisLabel: { color: ink, fontSize: 11 },
      },
      yAxis: [
        { type: 'value', scale: true, splitLine: { lineStyle: { color: line, type: 'dashed' } }, axisLabel: { color: ink, fontSize: 11 } },
        { type: 'value', scale: true, splitLine: { show: false }, axisLabel: { color: ink, fontSize: 11 } },
      ],
      dataZoom: [
        { type: 'inside' },
        { type: 'slider', height: 18, bottom: 8, borderColor: cssVar('--line'), textStyle: { color: ink } },
      ],
      series: [
        {
          name: 'PE(TTM)（左轴）',
          type: 'line',
          data: data.peTtm.slice(from).map((x) => (x !== null && x > 0 ? x : null)),
          showSymbol: false,
          connectNulls: false,
          lineStyle: { width: 1.8, color: cssVar('--accent') },
          itemStyle: { color: cssVar('--accent') },
        },
        {
          name: 'PB（右轴）',
          type: 'line',
          yAxisIndex: 1,
          data: data.pb.slice(from),
          showSymbol: false,
          lineStyle: { width: 1.5, color: cssVar('--t-hot-1') },
          itemStyle: { color: cssVar('--t-hot-1') },
        },
      ],
    }
    return { pe, pb, option, from: data.dates[from] }
  }, [data, theme])

  const n = prices.length
  const stats =
    n > 1
      ? {
          last: prices[n - 1],
          d1: prices[n - 1] / prices[n - 2] - 1,
          y1: n > 250 ? prices[n - 1] / prices[n - 251] - 1 : null,
          hi: Math.max(...prices.slice(-250)),
          lo: Math.min(...prices.slice(-250)),
        }
      : null

  const rangePos = stats && stats.hi > stats.lo ? ((stats.last - stats.lo) / (stats.hi - stats.lo)) * 100 : 50

  return (
    <div className="page">
      {/* 顶部标题 + 悬浮式证券搜索栏 */}
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="m-0">行情图表</h1>
          <p className="sub mt-1">
            本地日线与估值分位。股票含扩展数据时展示 K 线、成交量与 PE / PB；场内外基金展示收盘价或净值。
          </p>
        </div>

        <div ref={searchRef} className="relative w-full sm:w-80">
          <Search className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-[var(--ink-3)]" />
          <input
            placeholder="输入 6 位代码或名称搜索…"
            value={q}
            onFocus={() => hits.length > 0 && setOpenHits(true)}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && setOpenHits(false)}
            className="h-9 w-full rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] pr-8 pl-9 text-sm text-[var(--ink)] placeholder:text-[var(--ink-3)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[var(--accent-soft)] focus:outline-none"
          />
          {q && (
            <button
              type="button"
              onClick={() => {
                setQ('')
                setHits([])
                setOpenHits(false)
              }}
              className="absolute top-1/2 right-2.5 -translate-y-1/2 text-[var(--ink-3)] hover:text-[var(--ink)]"
              aria-label="清空"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}

          {openHits && hits.length > 0 && (
            <ul className="absolute top-full right-0 left-0 z-30 mt-1.5 max-h-80 list-none overflow-y-auto rounded-xl border border-[var(--line-strong)] bg-[var(--panel)] p-1.5 shadow-[var(--shadow-md)]">
              {hits.map((h) => (
                <li key={h.kind + h.code}>
                  <a
                    href={`#/quote/${h.kind}/${h.code}`}
                    onClick={() => {
                      setQ('')
                      setHits([])
                      setOpenHits(false)
                    }}
                    className="flex items-center justify-between gap-2 rounded-lg px-2.5 py-2 text-sm text-[var(--ink)] no-underline transition-colors hover:bg-[var(--panel-2)]"
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="mono font-semibold text-[var(--accent)]">{h.code}</span>
                      <span className="truncate">{h.name || '（无名称）'}</span>
                    </div>
                    <div className="flex shrink-0 items-center gap-1.5">
                      <span className="tag tag-muted">{KIND_TEXT[h.kind]}</span>
                      {!h.hasData && <span className="tag tag-warn">无数据</span>}
                    </div>
                  </a>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      {data && (
        <section className="panel overflow-hidden">
          {/* 证券信息与关键行情条 */}
          <div className="flex flex-wrap items-center justify-between gap-4 border-b border-[var(--line)] px-5 py-4">
            <div className="flex flex-wrap items-center gap-4">
              <div>
                <div className="flex items-center gap-2">
                  <span className="text-lg font-bold tracking-tight text-[var(--ink)]">{data.name || data.code}</span>
                  <span className="mono rounded bg-[var(--panel-2)] px-1.5 py-0.5 text-xs text-[var(--ink-2)]">
                    {data.code}
                  </span>
                  <span className="tag tag-muted">{KIND_TEXT[data.kind]}</span>
                </div>
                {stats && (
                  <div className="mt-1 flex flex-wrap items-center gap-2.5">
                    <span className="text-2xl font-bold tracking-tight mono text-[var(--ink)]">
                      {stats.last.toFixed(3)}
                    </span>
                    <span
                      className={`inline-flex items-center gap-0.5 rounded-full px-2 py-0.5 text-xs font-semibold mono ${
                        stats.d1 > 0
                          ? 'bg-[color-mix(in_srgb,#ef4444_14%,transparent)] text-[#ef4444]'
                          : stats.d1 < 0
                            ? 'bg-[color-mix(in_srgb,#10b981_14%,transparent)] text-[#10b981]'
                            : 'bg-[var(--panel-2)] text-[var(--ink-2)]'
                      }`}
                    >
                      {stats.d1 > 0 ? (
                        <ArrowUpRight className="h-3.5 w-3.5" />
                      ) : stats.d1 < 0 ? (
                        <ArrowDownRight className="h-3.5 w-3.5" />
                      ) : null}
                      {pct(stats.d1, 2)}
                    </span>
                    <span className="text-xs text-[var(--ink-3)] mono">{data.dates[n - 1]}</span>
                    {stats.y1 !== null && (
                      <span className="text-xs text-[var(--ink-2)]">
                        近一年{' '}
                        <strong className={`mono ${stats.y1 > 0 ? 'up' : stats.y1 < 0 ? 'down' : ''}`}>
                          {pct(stats.y1)}
                        </strong>
                      </span>
                    )}
                  </div>
                )}
              </div>

              {stats && (
                <div className="hidden border-l border-[var(--line)] pl-4 xl:block">
                  <div className="flex items-center justify-between gap-6 text-[11px] text-[var(--ink-3)]">
                    <span>52 周低 {stats.lo.toFixed(2)}</span>
                    <span>52 周高 {stats.hi.toFixed(2)}</span>
                  </div>
                  <div className="relative mt-1.5 h-1.5 w-44 rounded-full bg-[var(--bg-subtle)]">
                    <div
                      className="absolute top-1/2 h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-[var(--panel)] bg-[var(--accent)]"
                      style={{ left: `${Math.min(100, Math.max(0, rangePos))}%` }}
                    />
                  </div>
                </div>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <div className="seg" title="图表引擎（保存在本浏览器）">
                {RENDERERS.map((r) => (
                  <button key={r.id} className={r.id === renderer.id ? 'on' : ''} onClick={() => setRenderer(r.id)}>
                    {r.label}
                  </button>
                ))}
              </div>
              <div className="seg">
                {data.kind === 'ofund' ? (
                  <>
                    <button className={mode !== 'none' ? 'on' : ''} onClick={() => setMode('qfq')}>
                      累计净值
                    </button>
                    <button className={mode === 'none' ? 'on' : ''} onClick={() => setMode('none')}>
                      单位净值
                    </button>
                  </>
                ) : (
                  <>
                    <button className={mode === 'qfq' ? 'on' : ''} onClick={() => setMode('qfq')}>
                      前复权
                    </button>
                    <button className={mode === 'hfq' ? 'on' : ''} onClick={() => setMode('hfq')}>
                      后复权
                    </button>
                    <button className={mode === 'none' ? 'on' : ''} onClick={() => setMode('none')}>
                      不复权
                    </button>
                  </>
                )}
              </div>
            </div>
          </div>

          <div className="p-2">
            {kline ? (
              <Suspense fallback={<div className="empty" style={{ height: hasK ? 540 : 480 }}>加载图表…</div>}>
                <renderer.Component data={kline} height={hasK ? 540 : 480} />
              </Suspense>
            ) : (
              <div className="empty">本地没有这只证券的数据。</div>
            )}
          </div>

          {data.kind === 'stock' && !hasK && n > 0 && (
            <p className="disclaimer border-t border-[var(--line)] px-5 py-3">
              没有扩展数据（K 线、成交量、估值）：可在「数据同步」页回填扩展字段，或运行 <code className="mono">mt ext-backfill</code>。
            </p>
          )}
        </section>
      )}

      {data && fills.length > 0 && (
        <section className="panel overflow-hidden">
          <div className="panel-head border-b border-[var(--line)]">
            <strong>模拟盘成交复盘（{fills.length} 笔）</strong>
            <span className="muted text-xs">来自本地 A 股模拟盘已成交委托</span>
            <a href="#/sim" className="btn btn-ghost text-xs" style={{ marginLeft: 'auto' }}>
              前往模拟盘 →
            </a>
          </div>
          <div className="table-scroll">
            <table className="table static">
              <thead>
                <tr>
                  <th>成交日</th>
                  <th>账户</th>
                  <th>方向</th>
                  <th className="num">成交价</th>
                  <th className="num">数量</th>
                  <th className="num">金额</th>
                  <th>备注</th>
                </tr>
              </thead>
              <tbody>
                {fills.map((fl) => (
                  <tr key={fl.id}>
                    <td className="mono small">{fl.fillDate}</td>
                    <td className="small">{fl.accountName}</td>
                    <td>
                      <span className={`tag ${fl.side === 'buy' ? 'tag-hot' : 'tag-cold'}`}>{fl.side === 'buy' ? '买入' : '卖出'}</span>
                    </td>
                    <td className="num mono small">{fl.fillPrice.toFixed(3)}</td>
                    <td className="num mono small">{fl.qty.toLocaleString('zh-CN')}</td>
                    <td className="num mono small">{fl.amount.toLocaleString('zh-CN', { maximumFractionDigits: 0 })}</td>
                    <td className="muted small">{fl.note || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {valuation && (
        <section className="panel overflow-hidden">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3.5">
            <div className="flex flex-wrap items-center gap-3">
              <strong className="text-sm font-semibold">历史估值</strong>
              {valuation.pe && (
                <span className="inline-flex items-center gap-1.5 rounded-lg bg-[var(--bg-subtle)] px-2.5 py-1 text-xs">
                  <span className="text-[var(--ink-2)]">PE(TTM)</span>
                  <strong className="mono text-[var(--ink)]">{valuation.pe.last.toFixed(2)}</strong>
                  <span className="tag tag-cold">{valuation.pe.pct.toFixed(0)}% 分位</span>
                </span>
              )}
              {valuation.pb && (
                <span className="inline-flex items-center gap-1.5 rounded-lg bg-[var(--bg-subtle)] px-2.5 py-1 text-xs">
                  <span className="text-[var(--ink-2)]">PB</span>
                  <strong className="mono text-[var(--ink)]">{valuation.pb.last.toFixed(2)}</strong>
                  <span className="tag tag-hot">{valuation.pb.pct.toFixed(0)}% 分位</span>
                </span>
              )}
            </div>
            <span className="text-xs text-[var(--ink-3)]">
              自 {valuation.from} 起 · 来源 stockdb（亏损期 PE 不计入）
            </span>
          </div>
          <div className="p-3">
            <EChart option={valuation.option} height={320} />
          </div>
        </section>
      )}

      {data && <NotesBox target={`${data.kind}:${data.code}`} label={`${data.code} ${data.name}`} />}

      {!data && !error && (
        <div className="panel flex flex-col items-center justify-center gap-3 py-20 text-center">
          <span className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--panel-2)] text-[var(--ink-3)]">
            <CandlestickChart className="h-6 w-6" />
          </span>
          <div className="text-sm font-medium text-[var(--ink)]">搜索一只股票或基金查看走势</div>
          <p className="m-0 max-w-md text-xs text-[var(--ink-2)]">
            在右上角输入 6 位代码或简称，也可以随时按 <kbd className="mono rounded border border-[var(--line)] px-1.5 py-0.5">Ctrl K</kbd> 全局搜索直达。
          </p>
        </div>
      )}
    </div>
  )
}
