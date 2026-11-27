import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle, ArrowDownRight, ArrowRight, ArrowUpRight, Compass, Minus, Radio, RefreshCw, Thermometer, TrendingUp } from 'lucide-react'
import { api, type DayScore, type Num, type Overview as OverviewData, type RadarResp } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { EVENT_TEXT, fmt, PHASE_TEXT, TIER_COLOR, TIER_TEXT, zoneOf } from './temperature'
import { useTheme } from './theme'

/** 0–100 的渐变温度刻度条，带分区刻度和当前值发光指针。 */
function Scale({ value, marks, color }: { value: Num; marks: number[]; color: string }) {
  const clamped = value !== null ? Math.min(100, Math.max(0, value)) : null
  return (
    <div className="pt-2" aria-hidden>
      <div
        className="relative h-2.5 rounded-full"
        style={{
          background:
            'linear-gradient(to right, var(--t-cold-2) 0%, var(--t-cold-1) 28%, var(--t-neutral) 50%, var(--t-hot-1) 72%, var(--t-hot-2) 100%)',
        }}
      >
        {marks.map((m) => (
          <span
            key={m}
            className="absolute -top-0.5 h-3.5 w-[2px] -translate-x-1/2 rounded-full bg-[var(--panel)] opacity-85"
            style={{ left: `${m}%` }}
          />
        ))}
        {clamped !== null && (
          <span
            className="absolute top-1/2 h-4.5 w-4.5 -translate-x-1/2 -translate-y-1/2 rounded-full border-[3px] border-[var(--panel)] transition-all duration-300"
            style={{
              left: `${clamped}%`,
              background: color,
              boxShadow: `0 0 0 1px var(--line-strong), 0 0 12px ${color}`,
            }}
          />
        )}
      </div>
      <div className="relative mt-2 h-4 text-[11px] text-[var(--ink-3)] mono">
        <span className="absolute left-0">0</span>
        {marks.map((m) => (
          <span key={m} className="absolute -translate-x-1/2" style={{ left: `${m}%` }}>
            {m}
          </span>
        ))}
        <span className="absolute right-0">100</span>
      </div>
    </div>
  )
}

function Delta({ cur, prev }: { cur: Num; prev: Num }) {
  if (cur === null || prev === null) return null
  const d = cur - prev
  if (Math.abs(d) < 0.05) {
    return (
      <span className="inline-flex items-center gap-1 rounded-full bg-[var(--panel-2)] px-2 py-0.5 text-xs text-[var(--ink-2)] mono">
        <Minus className="h-3 w-3" />
        持平
      </span>
    )
  }
  const up = d > 0
  return (
    <span
      className={`inline-flex items-center gap-0.5 rounded-full px-2 py-0.5 text-xs font-medium mono ${
        up
          ? 'bg-[color-mix(in_srgb,var(--t-hot-2)_14%,transparent)] text-[var(--t-hot-2)]'
          : 'bg-[color-mix(in_srgb,var(--t-cold-2)_14%,transparent)] text-[var(--t-cold-2)]'
      }`}
    >
      {up ? <ArrowUpRight className="h-3.5 w-3.5" /> : <ArrowDownRight className="h-3.5 w-3.5" />}
      {up ? '+' : ''}
      {d.toFixed(1)}
    </span>
  )
}

type Dim = { name: string; score: Num; raw: string; enabled: boolean; hint: string }

function dims(d: DayScore, weights: number[]): Dim[] {
  return [
    {
      name: '资金热度',
      score: d.t[0],
      enabled: weights[0] > 0,
      raw: `换手率 ${fmt(d.heat, 2, '%')}`,
      hint: '中证全指成交额 ÷ 全A流通市值，5 日均值',
    },
    {
      name: '杠杆情绪',
      score: d.t[1],
      enabled: weights[1] > 0,
      raw: `融资买入占比 ${fmt(d.leverage, 1, '%')}`,
      hint: '融资买入额 ÷ 中证全指成交额，T-1，5 日均值',
    },
    {
      name: '市场广度',
      score: d.t[2],
      enabled: weights[2] > 0,
      raw: `站上 MA200 ${fmt(d.breadth, 1, '%')}`,
      hint: '全 A 股（含退市）后复权收盘价站上自身 200 日均线的占比',
    },
    {
      name: '价格动量',
      score: d.t[3],
      enabled: weights[3] > 0,
      raw: `日 RSI ${fmt(d.rsiD, 0)} / 周 RSI ${fmt(d.rsiW, 0)}`,
      hint: '0.4 × 日线 RSI(14) + 0.6 × 周线 RSI(14)',
    },
  ]
}

function OverviewSkeleton() {
  return (
    <div className="page animate-pulse">
      <div className="h-10 w-64 rounded-lg bg-[var(--panel)]" />
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <div className="h-48 rounded-xl border border-[var(--line)] bg-[var(--panel)]" />
        <div className="h-48 rounded-xl border border-[var(--line)] bg-[var(--panel)]" />
      </div>
      <div className="h-32 rounded-xl border border-[var(--line)] bg-[var(--panel)]" />
      <div className="h-72 rounded-xl border border-[var(--line)] bg-[var(--panel)]" />
    </div>
  )
}

export default function Overview() {
  const [data, setData] = useState<OverviewData | null>(null)
  const [radar, setRadar] = useState<RadarResp | null>(null)
  const [radarBusy, setRadarBusy] = useState(false)
  const [error, setError] = useState('')
  const { theme } = useTheme()

  const loadRadar = (refresh = false) => {
    setRadarBusy(true)
    api
      .radar(refresh)
      .then(setRadar, () => {})
      .finally(() => setRadarBusy(false))
  }

  useEffect(() => {
    api.overview().then(setData, (e) => setError(e.message))
    loadRadar(false)
  }, [])

  const chartOption = useMemo<EChartsOption | null>(() => {
    if (!data?.recent?.length) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const accent = cssVar('--accent')
    const tooltipBg = cssVar('--tooltip-bg') || cssVar('--panel')
    const tooltipBorder = cssVar('--tooltip-border') || cssVar('--line-strong')

    return {
      animation: false,
      grid: { left: 42, right: 58, top: 34, bottom: 28 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: tooltipBg,
        borderColor: tooltipBorder,
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        axisPointer: {
          type: 'line',
          lineStyle: { color: cssVar('--line-strong'), type: 'dashed' },
        },
      },
      legend: {
        top: 0,
        right: 0,
        textStyle: { color: ink, fontSize: 12 },
        itemWidth: 14,
        itemHeight: 3,
      },
      xAxis: {
        type: 'category',
        data: data.recent.map((r) => r.date),
        boundaryGap: false,
        axisLine: { lineStyle: { color: line } },
        axisTick: { show: false },
        axisLabel: { color: ink, fontSize: 11 },
      },
      yAxis: [
        {
          type: 'value',
          min: 0,
          max: 100,
          interval: 20,
          splitLine: { lineStyle: { color: line, type: 'dashed' } },
          axisLabel: { color: ink, fontSize: 11 },
        },
        {
          type: 'value',
          scale: true,
          splitLine: { show: false },
          axisLabel: { color: ink, fontSize: 11 },
        },
      ],
      series: [
        {
          name: '择时温度 T_s',
          type: 'line',
          showSymbol: false,
          smooth: 0.15,
          data: data.recent.map((r) => r.tS),
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
                { offset: 0, color: theme === 'dark' ? 'rgba(59, 130, 246, 0.24)' : 'rgba(37, 99, 235, 0.16)' },
                { offset: 1, color: 'rgba(59, 130, 246, 0.00)' },
              ],
            },
          },
          markArea: {
            silent: true,
            data: [
              [{ yAxis: 0, itemStyle: { color: cssVar('--t-cold-bg') } }, { yAxis: 20 }],
              [{ yAxis: 80, itemStyle: { color: cssVar('--t-hot-bg') } }, { yAxis: 100 }],
            ],
          },
        },
        {
          name: `${data.index} 收盘`,
          type: 'line',
          yAxisIndex: 1,
          showSymbol: false,
          data: data.recent.map((r) => r.close),
          lineStyle: { width: 1.4, color: ink, opacity: 0.75 },
          itemStyle: { color: ink },
        },
      ],
    }
  }, [data, theme])

  if (error) return <div className="error" role="alert">{error}</div>
  if (!data) return <OverviewSkeleton />
  const d = data.last
  if (!d) {
    return (
      <div className="page">
        <div className="page-head">
          <div>
            <h1>市场总览</h1>
          </div>
        </div>
        <div className="panel empty">还没有打分结果。请前往「数据同步」页执行一次同步，同步结束后会自动打分。</div>
      </div>
    )
  }

  const p = data.params
  const zone = zoneOf(d.tS)
  const tierColor = TIER_COLOR[d.tier] ?? 'var(--ink-2)'

  return (
    <div className="page">
      {/* 顶部标题与核心指数快照 */}
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <div className="flex items-center gap-2.5">
            <h1 className="m-0">A 股市场温度</h1>
            <span className="rounded-md border border-[var(--line-strong)] bg-[var(--panel)] px-2 py-0.5 text-xs font-medium text-[var(--ink-2)] mono">
              {data.index}
            </span>
          </div>
          <p className="mt-1.5 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-[var(--ink-2)]">
            <span className="mono">{d.date}</span>
            <span>·</span>
            <span>
              收盘{' '}
              <strong className="font-semibold text-[var(--ink)] mono">
                {d.close?.toLocaleString('zh-CN', { minimumFractionDigits: 2 })}
              </strong>
            </span>
            <span>·</span>
            <span className="mono text-[var(--ink-3)]">参数 {data.paramsVersion}</span>
          </p>
        </div>
        <div className="flex items-center gap-2">
          <a href="#/history" className="btn">
            历史走势
          </a>
          <a href="#/backtest" className="btn btn-primary">
            参数回测
          </a>
        </div>
      </div>

      {d.degraded && (
        <div className="notice flex items-center gap-2.5" role="status">
          <AlertTriangle className="h-4 w-4 shrink-0" />
          <span>当日有一个维度缺数，温度按其余维度重新加权计算，本日不应据此触发信号。</span>
        </div>
      )}

      {/* 核心温度计与估值双主卡 */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <section className="panel relative overflow-hidden p-5">
          <div
            className="pointer-events-none absolute inset-x-0 top-0 h-[2px]"
            style={{ background: zone?.color ?? 'var(--accent)' }}
            aria-hidden
          />
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1.5 text-xs font-semibold tracking-wide text-[var(--ink-2)] uppercase">
              <Thermometer className="h-3.5 w-3.5 text-[var(--accent)]" />
              择时温度 T_s
            </span>
            {zone && (
              <span
                className="tag"
                style={{
                  color: zone.color,
                  background: `color-mix(in srgb, ${zone.color} 14%, transparent)`,
                }}
              >
                {zone.label}
              </span>
            )}
          </div>

          <div className="mt-3 flex items-baseline gap-3">
            <span className="text-[46px] leading-none font-bold tracking-tight mono" style={{ color: zone?.color }}>
              {fmt(d.tS)}
            </span>
            <Delta cur={d.tS} prev={data.prev?.tS ?? null} />
          </div>

          <Scale value={d.tS} marks={[20, 35, 70, 80]} color={zone?.color ?? 'var(--ink-2)'} />

          <p className="mt-3 mb-0 text-xs leading-relaxed text-[var(--ink-2)]">
            各维度得分加权后再取近 3 年分位、3 日平滑。≤20 为深度冰点，≥80 为过热。
            {d.nLow > 0 && <span className="text-[var(--t-cold-2)]"> 当前 {d.nLow} 个维度处于冰点区。</span>}
            {d.nHigh > 0 && <span className="text-[var(--t-hot-2)]"> 当前 {d.nHigh} 个维度处于过热区。</span>}
          </p>
        </section>

        <section className="panel relative overflow-hidden p-5">
          <div
            className="pointer-events-none absolute inset-x-0 top-0 h-[2px]"
            style={{ background: tierColor }}
            aria-hidden
          />
          <div className="flex items-center justify-between">
            <span className="flex items-center gap-1.5 text-xs font-semibold tracking-wide text-[var(--ink-2)] uppercase">
              <TrendingUp className="h-3.5 w-3.5 text-[var(--accent)]" />
              估值背景 V
            </span>
            {d.tier && (
              <span
                className="tag"
                style={{
                  color: tierColor,
                  background: `color-mix(in srgb, ${tierColor} 14%, transparent)`,
                }}
              >
                {TIER_TEXT[d.tier]}
              </span>
            )}
          </div>

          <div className="mt-3 flex items-baseline gap-3">
            <span className="text-[46px] leading-none font-bold tracking-tight mono" style={{ color: tierColor }}>
              {fmt(d.v)}
            </span>
            <Delta cur={d.v} prev={data.prev?.v ?? null} />
          </div>

          <Scale value={d.v} marks={[p.cheapBelow, p.expensiveAbove]} color={tierColor} />

          <p className="mt-3 mb-0 text-xs leading-relaxed text-[var(--ink-2)]">
            股债利差 ERP{' '}
            <strong className="font-medium text-[var(--ink)] mono">
              {fmt(d.erp === null ? null : d.erp * 100, 2, '%')}
            </strong>
            （1/PE − 10 年国债收益率），近 10 年越高越便宜。V ≤ {p.cheapBelow} 为便宜，≥ {p.expensiveAbove} 为昂贵。
          </p>
        </section>
      </div>

      {/* 信号状态与最近事件 */}
      <section className="panel grid grid-cols-1 gap-5 p-5 md:grid-cols-[220px_1fr]">
        <div className="flex flex-col justify-between gap-2 md:border-r md:border-[var(--line)] md:pr-5">
          <div className="flex items-center gap-1.5 text-xs font-semibold tracking-wide text-[var(--ink-2)] uppercase">
            <Radio className="h-3.5 w-3.5 text-[var(--accent)]" />
            当前信号状态
          </div>
          <div className="flex items-center gap-2.5 py-1">
            <span className="relative flex h-2.5 w-2.5">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[var(--accent)] opacity-60" />
              <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-[var(--accent)]" />
            </span>
            <span className="text-xl font-semibold tracking-tight text-[var(--ink)]">
              {PHASE_TEXT[data.phase ?? ''] ?? '—'}
            </span>
          </div>
          <a
            href="#/signals"
            className="inline-flex items-center gap-1 text-xs font-medium text-[var(--accent)] no-underline hover:underline"
          >
            查看全部信号事件
            <ArrowRight className="h-3.5 w-3.5" />
          </a>
        </div>

        <div className="flex min-w-0 flex-col justify-center gap-2">
          {(data.recentEvents ?? []).length === 0 ? (
            <span className="text-xs text-[var(--ink-2)]">暂无历史触发事件</span>
          ) : (
            (data.recentEvents ?? []).map((e, i) => (
              <div
                key={i}
                className="flex flex-wrap items-center gap-2.5 rounded-lg bg-[var(--bg-subtle)] px-3 py-2 text-xs"
              >
                <span className="mono shrink-0 text-[var(--ink-2)]">{e.date}</span>
                <span
                  className={`tag ${
                    e.type === 'BUY_DIP' ? 'tag-cold' : e.type === 'SELL_PEAK' ? 'tag-hot' : 'tag-muted'
                  }`}
                >
                  {EVENT_TEXT[e.type] ?? e.type}
                  {e.observe ? '（观察）' : ''}
                </span>
                <span className="min-w-0 flex-1 truncate text-[var(--ink-2)]">{e.reason}</span>
              </div>
            ))
          )}
        </div>
      </section>

      {/* 择时层四维度拆解 */}
      <section className="panel overflow-hidden">
        <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-[var(--line)] px-5 py-3.5">
          <strong className="text-sm font-semibold">择时层各维度</strong>
          <span className="text-xs text-[var(--ink-2)]">0 = 历史最冷，100 = 历史最热（近 3 年分位）</span>
        </div>
        <div className="divide-y divide-[var(--line)]">
          {dims(d, p.weights).map((x) => {
            const z = zoneOf(x.score)
            return (
              <div
                key={x.name}
                className={`grid grid-cols-1 items-center gap-3 px-5 py-3.5 md:grid-cols-[240px_1fr_56px_170px] ${
                  x.enabled ? '' : 'opacity-55'
                }`}
              >
                <div>
                  <div className="text-sm font-medium text-[var(--ink)]">{x.name}</div>
                  <div className="mt-0.5 text-[11.5px] text-[var(--ink-2)]">{x.hint}</div>
                </div>

                <div>
                  {x.enabled ? (
                    <div className="h-2 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                      <div
                        className="h-full rounded-full transition-all duration-300"
                        style={{
                          width: `${x.score ?? 0}%`,
                          background: z?.color ?? 'var(--line-strong)',
                        }}
                      />
                    </div>
                  ) : (
                    <span className="tag tag-muted">未启用（权重 0）</span>
                  )}
                </div>

                <div
                  className="text-right text-base font-semibold mono"
                  style={{ color: x.enabled ? z?.color : 'var(--ink-3)' }}
                >
                  {x.enabled ? fmt(x.score, 0) : '—'}
                </div>

                <div className="text-right text-xs text-[var(--ink-2)] mono">{x.raw}</div>
              </div>
            )
          })}
        </div>
      </section>

      {/* 多宽基估值与趋势雷达 */}
      <section className="panel overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] px-5 py-3.5">
          <div className="flex flex-wrap items-center gap-2">
            <Compass className="h-4 w-4 text-[var(--accent)]" />
            <strong className="text-sm font-semibold">多宽基估值与趋势雷达</strong>
            <span className="text-xs text-[var(--ink-2)]">
              中证官网实时（近 10 年 PE 分位{radar?.bondY10 != null ? ` · 10Y国债 ${radar.bondY10.toFixed(2)}%` : ''}）
            </span>
            {radar?.trSpread5y != null && (
              <span className="tag tag-ok mono" title="沪深300全收益(H00300) − 沪深300价格指数 近5年年化分红再投差">
                300 红利复利 +{((radar.trSpread5y ?? 0) * 100).toFixed(2)}%/年
              </span>
            )}
          </div>
          <button className="btn btn-ghost" disabled={radarBusy} onClick={() => loadRadar(true)}>
            <RefreshCw className={`h-3.5 w-3.5 ${radarBusy ? 'animate-spin' : ''}`} />
            刷新
          </button>
        </div>
        {radar ? (
          <div className="table-scroll">
            <table className="table static">
              <thead>
                <tr>
                  <th>指数</th>
                  <th>风格</th>
                  <th className="num">最新收盘</th>
                  <th className="num">PE(TTM)</th>
                  <th className="num">股债利差 ERP</th>
                  <th className="num">5年 / 10年 PE分位</th>
                  <th className="num">20日 / 60日 / 1年</th>
                  <th className="num">距 MA200</th>
                  <th>估值位置</th>
                </tr>
              </thead>
              <tbody>
                {radar.items.map((x) => {
                  const pct10 = x.pePct10Y
                  const pctColor =
                    pct10 === null ? 'var(--ink-2)' : pct10 <= 25 ? 'var(--t-cold-2)' : pct10 >= 75 ? 'var(--t-hot-2)' : 'var(--accent)'
                  const pFmt = (v: Num) => (v === null ? '—' : `${v >= 0 ? '+' : ''}${(v * 100).toFixed(1)}%`)
                  return (
                    <tr key={x.code}>
                      <td>
                        <div className="font-medium text-[var(--ink)]">{x.name}</div>
                        <div className="mono text-[11px] text-[var(--ink-3)]">
                          {x.code} {x.date && `· ${x.date}`}
                        </div>
                      </td>
                      <td>
                        <span className={`tag ${x.isTR ? 'tag-ok' : 'tag-muted'}`}>{x.style}</span>
                      </td>
                      {x.error ? (
                        <td colSpan={7} className="muted small">
                          拉取失败：{x.error}
                        </td>
                      ) : (
                        <>
                          <td className="num mono">{x.close.toFixed(2)}</td>
                          <td className="num mono">{x.pe !== null ? x.pe.toFixed(2) : '—'}</td>
                          <td className="num mono">{x.erp !== null ? `${x.erp.toFixed(2)}%` : '—'}</td>
                          <td className="num mono small">
                            <div>
                              {x.pePct5Y !== null ? `${x.pePct5Y.toFixed(0)}%` : '—'} /{' '}
                              <strong style={{ color: pctColor }}>{pct10 !== null ? `${pct10.toFixed(0)}%` : '—'}</strong>
                            </div>
                            {pct10 !== null && (
                              <div className="mt-1 h-1.5 w-24 overflow-hidden rounded-full bg-[var(--bg-subtle)] ml-auto">
                                <div className="h-full rounded-full" style={{ width: `${pct10}%`, background: pctColor }} />
                              </div>
                            )}
                          </td>
                          <td className="num mono small nowrap">
                            <span className={(x.ret20d ?? 0) >= 0 ? 'up' : 'down'}>{pFmt(x.ret20d)}</span>
                            {' / '}
                            <span className={(x.ret60d ?? 0) >= 0 ? 'up' : 'down'}>{pFmt(x.ret60d)}</span>
                            {' / '}
                            <span className={(x.ret250d ?? 0) >= 0 ? 'up' : 'down'}>{pFmt(x.ret250d)}</span>
                          </td>
                          <td className={`num mono small ${(x.ma200Dist ?? 0) >= 0 ? 'up' : 'down'}`}>{pFmt(x.ma200Dist)}</td>
                          <td>
                            {x.tier ? (
                              <span
                                className="tag"
                                style={{
                                  color: TIER_COLOR[x.tier] ?? 'var(--ink-2)',
                                  background: `color-mix(in srgb, ${TIER_COLOR[x.tier] ?? 'var(--ink-2)'} 14%, transparent)`,
                                }}
                              >
                                {TIER_TEXT[x.tier] ?? x.tier}
                              </span>
                            ) : (
                              '—'
                            )}
                          </td>
                        </>
                      )}
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        ) : (
          <div className="empty">{radarBusy ? '正在拉取多宽基中证指数估值数据…' : '暂未加载宽基雷达，点击右上角刷新。'}</div>
        )}
      </section>

      {/* 近一年趋势图 */}
      {chartOption && (
        <section className="panel overflow-hidden">
          <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-[var(--line)] px-5 py-3.5">
            <strong className="text-sm font-semibold">近一年温度与指数走势</strong>
            <span className="text-xs text-[var(--ink-2)]">蓝区为冰点（≤20），红区为过热（≥80）</span>
          </div>
          <div className="p-3">
            <EChart option={chartOption} height={320} />
          </div>
        </section>
      )}

      <p className="disclaimer">
        温度与估值档仅描述市场相对历史的位置，阈值需在「策略回测」页校准，当前默认参数的回测表现并不理想，不构成投资建议。
      </p>
    </div>
  )
}
