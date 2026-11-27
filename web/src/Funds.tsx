import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ArrowDownRight, ArrowUpRight, Layers, Plus, RefreshCw, Search, Trash2, X } from 'lucide-react'
import { api, ApiError, type FundInfo, type FundNAV, type JobStatus, type WatchedFund } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { pct } from './temperature'
import { useTheme } from './theme'

const RANGES = [
  { key: '3m', label: '3 月', days: 90 },
  { key: '1y', label: '1 年', days: 365 },
  { key: '3y', label: '3 年', days: 365 * 3 },
  { key: 'all', label: '全部', days: 0 },
] as const

type RangeKey = (typeof RANGES)[number]['key']

function maxDrawdown(vals: number[]): number | null {
  if (vals.length < 2) return null
  let peak = vals[0]
  let dd = 0
  for (const v of vals) {
    if (v > peak) peak = v
    if (peak > 0) {
      const cur = v / peak - 1
      if (cur < dd) dd = cur
    }
  }
  return dd
}

function retBefore(navs: FundNAV[], daysAgo: number): number | null {
  if (navs.length < 2) return null
  const last = navs[navs.length - 1]
  const target = new Date(last.date + 'T00:00:00Z')
  target.setUTCDate(target.getUTCDate() - daysAgo)
  const iso = target.toISOString().slice(0, 10)
  const base = navs.find((n) => n.date >= iso)
  if (!base || base === last || base.accNav <= 0) return null
  return last.accNav / base.accNav - 1
}

export default function Funds() {
  const [watched, setWatched] = useState<WatchedFund[]>([])
  const [infoCount, setInfoCount] = useState(0)
  const [configured, setConfigured] = useState(true)
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<FundInfo[]>([])
  const [selected, setSelected] = useState('')
  const [navs, setNavs] = useState<FundNAV[]>([])
  const [range, setRange] = useState<RangeKey>('1y')
  const [job, setJob] = useState<JobStatus | null>(null)
  const [error, setError] = useState('')
  const [openHits, setOpenHits] = useState(false)
  const searchRef = useRef<HTMLDivElement>(null)
  const { theme } = useTheme()

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (searchRef.current && !searchRef.current.contains(e.target as Node)) setOpenHits(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  const load = useCallback(() => {
    api.funds().then(
      (r) => {
        setWatched(r.watched)
        setInfoCount(r.infoCount)
        setConfigured(r.configured)
        setSelected((cur) => (cur && r.watched.some((f) => f.code === cur) ? cur : r.watched[0]?.code || ''))
      },
      (e) => setError(e.message),
    )
  }, [])

  useEffect(load, [load])

  useEffect(() => {
    if (!q.trim()) {
      setHits([])
      setOpenHits(false)
      return
    }
    const t = setTimeout(
      () =>
        api.searchFunds(q).then(
          (r) => {
            setHits(r)
            setOpenHits(true)
          },
          () => {},
        ),
      220,
    )
    return () => clearTimeout(t)
  }, [q])

  useEffect(() => {
    if (!selected) {
      setNavs([])
      return
    }
    api.fundNAV(selected).then(setNavs, (e) => setError(e.message))
  }, [selected])

  const running = job?.state === 'running'
  useEffect(() => {
    if (!running) return
    const t = setInterval(() => {
      api.currentJob().then(
        (j) => {
          setJob(j)
          if (j?.state !== 'running') {
            load()
            if (selected) api.fundNAV(selected).then(setNavs, () => {})
          }
        },
        () => {},
      )
    }, 1500)
    return () => clearInterval(t)
  }, [running, load, selected])

  async function run(f: () => Promise<unknown>) {
    setError('')
    try {
      await f()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }

  const add = (code: string) =>
    run(async () => {
      await api.watchFund(code)
      setQ('')
      setHits([])
      setOpenHits(false)
      setSelected(code)
      load()
    })

  const removeFund = (code: string) =>
    run(async () => {
      await api.unwatchFund(code)
      if (selected === code) {
        setSelected('')
        setNavs([])
      }
      load()
    })

  const sync = () => run(async () => setJob(await api.startFunds()))

  const current = watched.find((f) => f.code === selected)

  const filteredNavs = useMemo(() => {
    if (navs.length === 0) return []
    const r = RANGES.find((x) => x.key === range)!
    if (r.days === 0) return navs
    const last = navs[navs.length - 1]
    const cutoff = new Date(last.date + 'T00:00:00Z')
    cutoff.setUTCDate(cutoff.getUTCDate() - r.days)
    const iso = cutoff.toISOString().slice(0, 10)
    const idx = navs.findIndex((n) => n.date >= iso)
    return idx <= 0 ? navs : navs.slice(idx)
  }, [navs, range])

  const stats = useMemo(() => {
    if (navs.length < 2) return null
    const sub = filteredNavs.length >= 2 ? filteredNavs : navs
    const acc = sub.map((n) => n.accNav).filter((x) => x > 0)
    const spanRet = acc.length >= 2 ? acc[acc.length - 1] / acc[0] - 1 : null
    return {
      m1: retBefore(navs, 30),
      y1: retBefore(navs, 365),
      spanRet,
      mdd: maxDrawdown(acc),
      from: sub[0].date,
      to: sub[sub.length - 1].date,
    }
  }, [navs, filteredNavs])

  const option = useMemo<EChartsOption | null>(() => {
    if (filteredNavs.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const accent = cssVar('--accent')
    return {
      animation: false,
      grid: { left: 54, right: 24, top: 34, bottom: 52 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
      },
      legend: { top: 0, right: 0, textStyle: { color: ink, fontSize: 12 }, itemWidth: 14, itemHeight: 3 },
      xAxis: {
        type: 'category',
        data: filteredNavs.map((n) => n.date),
        boundaryGap: false,
        axisLine: { lineStyle: { color: line } },
        axisTick: { show: false },
        axisLabel: { color: ink, fontSize: 11 },
      },
      yAxis: {
        type: 'value',
        scale: true,
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
        axisLabel: { color: ink, fontSize: 11 },
      },
      dataZoom: [
        { type: 'inside' },
        { type: 'slider', height: 18, bottom: 8, borderColor: cssVar('--line'), textStyle: { color: ink } },
      ],
      series: [
        {
          name: '累计净值',
          type: 'line',
          showSymbol: false,
          data: filteredNavs.map((n) => n.accNav),
          lineStyle: { width: 2.2, color: accent },
          itemStyle: { color: accent },
          areaStyle: {
            color: {
              type: 'linear',
              x: 0,
              y: 0,
              x2: 0,
              y2: 1,
              colorStops: [
                { offset: 0, color: theme === 'dark' ? 'rgba(59, 130, 246, 0.22)' : 'rgba(37, 99, 235, 0.14)' },
                { offset: 1, color: 'rgba(59, 130, 246, 0.00)' },
              ],
            },
          },
        },
        {
          name: '单位净值',
          type: 'line',
          showSymbol: false,
          data: filteredNavs.map((n) => n.nav),
          lineStyle: { width: 1.4, color: ink, type: 'dashed' },
          itemStyle: { color: ink },
        },
      ],
    }
  }, [filteredNavs, theme])

  const kpiColor = (v: number | null) => (v === null ? '' : v > 0 ? 'up' : v < 0 ? 'down' : '')

  return (
    <div className="page">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="m-0">基金自选</h1>
          <p className="sub mt-1">
            场外基金按自选清单每日同步净值；场内 ETF / LOF 日线随个股同步。
            {infoCount > 0 && <span className="mono"> · 本地清单 {infoCount.toLocaleString('zh-CN')} 只</span>}
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2.5">
          <div ref={searchRef} className="relative w-64 sm:w-80">
            <Search className="pointer-events-none absolute top-1/2 left-3 h-3.5 w-3.5 -translate-y-1/2 text-[var(--ink-3)]" />
            <input
              placeholder={infoCount > 0 ? '搜索基金代码或名称添加…' : '输入 6 位代码回车添加…'}
              value={q}
              onFocus={() => hits.length > 0 && setOpenHits(true)}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && /^\d{6}$/.test(q.trim())) add(q.trim())
                if (e.key === 'Escape') setOpenHits(false)
              }}
              className="h-8.5 w-full rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] pr-8 pl-9 text-sm text-[var(--ink)] placeholder:text-[var(--ink-3)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[var(--accent-soft)] focus:outline-none"
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
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
            {openHits && hits.length > 0 && (
              <ul className="absolute top-full right-0 left-0 z-30 mt-1.5 max-h-80 list-none overflow-y-auto rounded-xl border border-[var(--line-strong)] bg-[var(--panel)] p-1.5 shadow-[var(--shadow-md)]">
                {hits.map((f) => {
                  const exists = watched.some((w) => w.code === f.code)
                  return (
                    <li
                      key={f.code}
                      className="flex items-center justify-between gap-2 rounded-lg px-2.5 py-2 text-sm hover:bg-[var(--panel-2)]"
                    >
                      <div className="min-w-0 truncate">
                        <span className="mono font-semibold text-[var(--accent)]">{f.code}</span> {f.name}{' '}
                        <span className="muted text-xs">{f.type}</span>
                      </div>
                      <button
                        className="btn btn-ghost shrink-0"
                        onClick={() => add(f.code)}
                        disabled={exists}
                      >
                        <Plus className="h-3.5 w-3.5" />
                        {exists ? '已添加' : '添加'}
                      </button>
                    </li>
                  )
                })}
              </ul>
            )}
          </div>
          <button className="btn btn-primary" disabled={running || !configured} onClick={sync}>
            <RefreshCw className={`h-3.5 w-3.5 ${running ? 'animate-spin' : ''}`} />
            {running ? '同步中…' : infoCount === 0 ? '下载清单并同步' : '同步净值'}
          </button>
        </div>
      </div>

      {!configured && <div className="notice">未配置场外基金数据源（config.yaml 的 funds.sources）。</div>}
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      {job && job.kind === 'funds' && job.log && job.log.length > 0 && <pre className="log panel">{job.log.join('\n')}</pre>}

      {watched.length === 0 ? (
        <div className="panel flex flex-col items-center justify-center gap-3 py-20 text-center">
          <span className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--panel-2)] text-[var(--ink-3)]">
            <Layers className="h-6 w-6" />
          </span>
          <div className="text-sm font-medium text-[var(--ink)]">还没有自选基金</div>
          <p className="m-0 text-xs text-[var(--ink-2)]">在右上角搜索基金代码或名称添加，然后点击「同步净值」获取历史曲线。</p>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[310px_1fr] lg:items-start">
          {/* 左侧自选基金卡片列表 */}
          <aside className="panel overflow-hidden">
            <div className="flex items-center justify-between border-b border-[var(--line)] px-4 py-3 text-xs font-semibold text-[var(--ink-2)]">
              <span>自选清单</span>
              <span className="tag tag-muted">{watched.length} 只</span>
            </div>
            <div className="divide-y divide-[var(--line)]">
              {watched.map((f) => {
                const active = f.code === selected
                return (
                  <div
                    key={f.code}
                    onClick={() => setSelected(f.code)}
                    role="button"
                    tabIndex={0}
                    onKeyDown={(e) => e.key === 'Enter' && setSelected(f.code)}
                    className={`group flex cursor-pointer items-center justify-between gap-3 px-4 py-3 transition-colors ${
                      active ? 'bg-[var(--accent-soft)]' : 'hover:bg-[var(--panel-2)]'
                    }`}
                  >
                    <div className="min-w-0 flex-1">
                      <div className={`truncate text-sm font-medium ${active ? 'text-[var(--accent)]' : 'text-[var(--ink)]'}`}>
                        {f.name || '（名称未知）'}
                      </div>
                      <div className="mt-0.5 flex items-center gap-2 text-xs text-[var(--ink-2)]">
                        <span className="mono">{f.code}</span>
                        {f.type && <span className="truncate text-[var(--ink-3)]">· {f.type}</span>}
                      </div>
                    </div>
                    <div className="shrink-0 text-right">
                      <div className="mono text-sm font-semibold text-[var(--ink)]">
                        {f.count ? f.accNav.toFixed(4) : '—'}
                      </div>
                      <div className="mono text-[11px] text-[var(--ink-3)]">{f.count ? f.last : '未同步'}</div>
                    </div>
                  </div>
                )
              })}
            </div>
          </aside>

          {/* 右侧净值分析主工作区 */}
          <div className="flex min-w-0 flex-col gap-4">
            {current && (
              <>
                <section className="panel p-5">
                  <div className="flex flex-wrap items-start justify-between gap-4">
                    <div>
                      <div className="flex flex-wrap items-center gap-2">
                        <h2 className="m-0 text-lg font-bold tracking-tight text-[var(--ink)]">
                          {current.name || current.code}
                        </h2>
                        <span className="mono rounded bg-[var(--panel-2)] px-1.5 py-0.5 text-xs text-[var(--ink-2)]">
                          {current.code}
                        </span>
                        {current.type && <span className="tag tag-muted">{current.type}</span>}
                      </div>
                      <div className="mt-2 flex flex-wrap items-baseline gap-4 text-xs text-[var(--ink-2)]">
                        <span>
                          累计净值{' '}
                          <strong className="mono text-xl font-bold text-[var(--ink)]">
                            {current.count ? current.accNav.toFixed(4) : '—'}
                          </strong>
                        </span>
                        <span>
                          单位净值 <strong className="mono text-sm text-[var(--ink)]">{current.count ? current.nav.toFixed(4) : '—'}</strong>
                        </span>
                        {current.count > 0 && (
                          <span className="mono text-[var(--ink-3)]">
                            {current.first} ~ {current.last}（{current.count.toLocaleString('zh-CN')} 条）
                          </span>
                        )}
                      </div>
                    </div>
                    <button
                      className="btn btn-ghost text-[var(--bad)]"
                      onClick={() => removeFund(current.code)}
                      title="移出自选"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      移除
                    </button>
                  </div>

                  {/* 4 列基金绩效 KPI */}
                  {stats && (
                    <div className="mt-4 grid grid-cols-2 gap-3 border-t border-[var(--line)] pt-4 sm:grid-cols-4">
                      <div>
                        <div className="text-xs text-[var(--ink-2)]">近 1 月</div>
                        <div className={`mt-1 flex items-center gap-0.5 text-lg font-bold mono ${kpiColor(stats.m1)}`}>
                          {stats.m1 !== null && (stats.m1 >= 0 ? <ArrowUpRight className="h-4 w-4" /> : <ArrowDownRight className="h-4 w-4" />)}
                          {pct(stats.m1)}
                        </div>
                      </div>
                      <div>
                        <div className="text-xs text-[var(--ink-2)]">近 1 年</div>
                        <div className={`mt-1 flex items-center gap-0.5 text-lg font-bold mono ${kpiColor(stats.y1)}`}>
                          {stats.y1 !== null && (stats.y1 >= 0 ? <ArrowUpRight className="h-4 w-4" /> : <ArrowDownRight className="h-4 w-4" />)}
                          {pct(stats.y1)}
                        </div>
                      </div>
                      <div>
                        <div className="text-xs text-[var(--ink-2)]">当前区间累计</div>
                        <div className={`mt-1 text-lg font-bold mono ${kpiColor(stats.spanRet)}`}>{pct(stats.spanRet)}</div>
                      </div>
                      <div>
                        <div className="text-xs text-[var(--ink-2)]">区间最大回撤</div>
                        <div className="mt-1 text-lg font-bold mono down">{pct(stats.mdd)}</div>
                      </div>
                    </div>
                  )}
                </section>

                <section className="panel overflow-hidden">
                  <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3">
                    <div className="flex items-center gap-2">
                      <strong className="text-sm font-semibold">净值曲线</strong>
                      <span className="text-xs text-[var(--ink-2)]">累计净值含分红，适合评估长期收益</span>
                    </div>
                    <div className="seg" role="group" aria-label="净值区间">
                      {RANGES.map((r) => (
                        <button key={r.key} className={r.key === range ? 'on' : ''} onClick={() => setRange(r.key)}>
                          {r.label}
                        </button>
                      ))}
                    </div>
                  </div>
                  <div className="p-3">
                    {option ? (
                      <EChart option={option} height={400} />
                    ) : (
                      <div className="empty">尚无净值数据，请点击右上角「同步净值」。</div>
                    )}
                  </div>
                </section>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
