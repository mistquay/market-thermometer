import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle, ArrowDownRight, ArrowRight, ArrowUpRight, Compass, Minus, Radio, RefreshCw, Thermometer, TrendingUp } from 'lucide-react'
import { api, type DayScore, type Num, type Overview as OverviewData, type RadarResp } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { EVENT_TEXT, fmt, PHASE_TEXT, TIER_COLOR, TIER_TEXT, zoneOf } from './temperature'
import { useTheme } from './theme'

type GaugeZone = { from: number; to: number; color: string; label?: string }

/** 180° 金融级半圆弧形温度/估值仪表盘（SVG 精绘，支持明暗双主题）。 */
function GaugeMeter({
  value,
  prev,
  color,
  zones,
  marks,
}: {
  value: Num
  prev: Num
  color: string
  zones: GaugeZone[]
  marks: number[]
}) {
  const clamped = value !== null ? Math.min(100, Math.max(0, value)) : null
  const cx = 140
  const cy = 122
  const r = 92
  const strokeW = 13

  const polar = (v: number, rad = r) => {
    const radAngle = Math.PI * (1 - v / 100)
    return { x: cx + rad * Math.cos(radAngle), y: cy - rad * Math.sin(radAngle) }
  }

  const arcPath = (from: number, to: number) => {
    const gap = 0.7
    const a = Math.min(to, from + (from === 0 ? 0 : gap))
    const b = Math.max(a, to - (to === 100 ? 0 : gap))
    const p1 = polar(a)
    const p2 = polar(b)
    return `M ${p1.x.toFixed(2)} ${p1.y.toFixed(2)} A ${r} ${r} 0 0 1 ${p2.x.toFixed(2)} ${p2.y.toFixed(2)}`
  }

  const needle = clamped !== null ? polar(clamped, r) : null
  const needleTip = clamped !== null ? polar(clamped, r - 18) : null

  return (
    <div className="relative mx-auto flex w-full max-w-[300px] flex-col items-center pt-1">
      <svg viewBox="0 0 280 148" className="h-[144px] w-full overflow-visible" aria-hidden>
        {/* 轨道底环 */}
        <path
          d={`M ${cx - r} ${cy} A ${r} ${r} 0 0 1 ${cx + r} ${cy}`}
          fill="none"
          stroke="var(--line)"
          strokeWidth={strokeW}
          strokeLinecap="round"
        />
        {/* 分区色段 */}
        {zones.map((z) => (
          <path
            key={`${z.from}-${z.to}`}
            d={arcPath(z.from, z.to)}
            fill="none"
            stroke={z.color}
            strokeWidth={strokeW}
            strokeLinecap="butt"
            opacity={0.85}
          />
        ))}
        {/* 阈值刻度线与数字 */}
        {[0, ...marks, 100].map((m) => {
          const pIn = polar(m, r - strokeW / 2 - 3)
          const pOut = polar(m, r + strokeW / 2 + 2)
          const pLbl = polar(m, r + strokeW / 2 + 13)
          return (
            <g key={m}>
              <line
                x1={pIn.x}
                y1={pIn.y}
                x2={pOut.x}
                y2={pOut.y}
                stroke="var(--line-strong)"
                strokeWidth={m === 0 || m === 100 ? 1 : 1.5}
              />
              <text
                x={pLbl.x}
                y={pLbl.y + 3}
                textAnchor="middle"
                fill="var(--ink-3)"
                fontSize="10"
                className="mono"
              >
                {m}
              </text>
            </g>
          )
        })}
        {/* 当前值发光指针 */}
        {needle && needleTip && (
          <g>
            <line
              x1={needleTip.x}
              y1={needleTip.y}
              x2={needle.x}
              y2={needle.y}
              stroke={color}
              strokeWidth={3}
              strokeLinecap="round"
            />
            <circle
              cx={needle.x}
              cy={needle.y}
              r={7.5}
              fill={color}
              stroke="var(--panel)"
              strokeWidth={3}
              style={{ filter: `drop-shadow(0 0 6px ${color})` }}
            />
          </g>
        )}
      </svg>

      {/* 仪表中心数值与较昨日变化 */}
      <div className="-mt-[68px] flex flex-col items-center">
        <span className="text-[42px] leading-none font-bold tracking-tight mono" style={{ color }}>
          {fmt(value)}
        </span>
        <div className="mt-1.5">
          <Delta cur={value} prev={prev} />
        </div>
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
        较前日持平
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
      <span className="ml-0.5 text-[10px] opacity-80">较前日</span>
    </span>
  )
}

type Dim = { name: string; code: string; score: Num; weight: number; raw: string; enabled: boolean; hint: string }

function dims(d: DayScore, weights: number[]): Dim[] {
  return [
    {
      name: '资金热度',
      code: 'T1',
      score: d.t[0],
      weight: weights[0] ?? 0,
      enabled: weights[0] > 0,
      raw: `换手率 ${fmt(d.heat, 2, '%')}`,
      hint: '中证全指成交额 ÷ 全A流通市值，5 日均值',
    },
    {
      name: '杠杆情绪',
      code: 'T2',
      score: d.t[1],
      weight: weights[1] ?? 0,
      enabled: weights[1] > 0,
      raw: `融资买入占比 ${fmt(d.leverage, 1, '%')}`,
      hint: '融资买入额 ÷ 中证全指成交额，T-1，5 日均值',
    },
    {
      name: '市场广度',
      code: 'T3',
      score: d.t[2],
      weight: weights[2] ?? 0,
      enabled: weights[2] > 0,
      raw: `站上 MA200 ${fmt(d.breadth, 1, '%')}`,
      hint: '全 A 股（含退市）后复权收盘价站上自身 200 日均线的占比',
    },
    {
      name: '价格动量',
      code: 'T4',
      score: d.t[3],
      weight: weights[3] ?? 0,
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
        <div className="h-60 rounded-xl border border-[var(--line)] bg-[var(--panel)]" />
        <div className="h-60 rounded-xl border border-[var(--line)] bg-[var(--panel)]" />
      </div>
      <div className="h-36 rounded-xl border border-[var(--line)] bg-[var(--panel)]" />
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

  // 多宽基估值-趋势四象限散点气泡图
  const radarOption = useMemo<EChartsOption | null>(() => {
    if (!radar?.items?.length) return null
    const valid = radar.items.filter((x) => !x.error && x.pePct10Y !== null && x.ma200Dist !== null)
    if (valid.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const cold = cssVar('--t-cold-2')
    const hot = cssVar('--t-hot-2')
    const accent = cssVar('--accent')
    const pts = valid.map((x) => {
      const pct10 = x.pePct10Y!
      const dist = +(x.ma200Dist! * 100).toFixed(1)
      const erp = x.erp ?? 3
      const color = pct10 <= 30 ? cold : pct10 >= 70 ? hot : accent
      return {
        name: x.name,
        value: [+pct10.toFixed(1), dist, +erp.toFixed(2), x.pe ?? 0, x.pePct5Y ?? 0, (x.ret60d ?? 0) * 100],
        symbolSize: Math.max(14, Math.min(34, 12 + Math.max(0, erp) * 2.4)),
        itemStyle: { color, opacity: 0.86, borderColor: cssVar('--panel'), borderWidth: 2 },
      }
    })
    const yVals = pts.map((p) => p.value[1])
    const yMin = Math.min(-10, Math.floor((Math.min(...yVals) - 6) / 5) * 5)
    const yMax = Math.max(10, Math.ceil((Math.max(...yVals) + 6) / 5) * 5)

    return {
      animation: false,
      grid: { left: 52, right: 26, top: 28, bottom: 42 },
      tooltip: {
        trigger: 'item',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        formatter: (p: unknown) => {
          const d = p as { name: string; value: number[] }
          return [
            `<strong>${d.name}</strong>`,
            `10年 PE 分位：<b>${d.value[0]}%</b>（5年 ${d.value[4].toFixed(0)}%）`,
            `距 MA200：<b>${d.value[1] >= 0 ? '+' : ''}${d.value[1]}%</b>`,
            `PE(TTM)：${d.value[3].toFixed(2)} · ERP：${d.value[2].toFixed(2)}%`,
            `60日涨跌：${d.value[5] >= 0 ? '+' : ''}${d.value[5].toFixed(1)}%`,
          ].join('<br/>')
        },
      },
      xAxis: {
        type: 'value',
        name: '10年 PE 分位 (%)',
        nameLocation: 'middle',
        nameGap: 24,
        nameTextStyle: { color: ink, fontSize: 11 },
        min: 0,
        max: 100,
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v}%` },
      },
      yAxis: {
        type: 'value',
        name: '距 MA200 (%)',
        nameTextStyle: { color: ink, fontSize: 11 },
        min: yMin,
        max: yMax,
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v > 0 ? '+' : ''}${v}%` },
      },
      series: [
        {
          type: 'scatter',
          data: pts,
          label: {
            show: true,
            formatter: '{b}',
            position: 'top',
            distance: 5,
            color: inkMain,
            fontSize: 11,
            fontWeight: 500,
          },
          markArea: {
            silent: true,
            data: [
              [{ xAxis: 0, itemStyle: { color: cssVar('--t-cold-bg') } }, { xAxis: 30 }],
              [{ xAxis: 70, itemStyle: { color: cssVar('--t-hot-bg') } }, { xAxis: 100 }],
            ],
          },
          markLine: {
            silent: true,
            symbol: 'none',
            lineStyle: { color: cssVar('--line-strong'), type: 'dashed', width: 1 },
            label: { color: cssVar('--ink-3'), fontSize: 10 },
            data: [
              { xAxis: 30, label: { formatter: '便宜 30%', position: 'insideEndTop' } },
              { xAxis: 70, label: { formatter: '昂贵 70%', position: 'insideEndTop' } },
              { yAxis: 0, label: { formatter: 'MA200 均线', position: 'insideEndTop' } },
            ],
          },
        },
      ],
    }
  }, [radar, theme])

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
  const dimList = dims(d, p.weights)
  const posBasePct = data.position ? Math.round(data.position.base * 100) : 0
  const posOverlayPct = data.position ? Math.round(data.position.overlay * 100) : 0
  const posFinalPct = data.position ? Math.round(data.position.weight * 100) : 0

  return (
    <div className="page">
      {/* 顶部标题与核心指数快照 */}
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2.5">
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
        <div className="flex flex-wrap items-center gap-2">
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

      {/* 核心温度计与估值双半圆仪表盘 */}
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

          <GaugeMeter
            value={d.tS}
            prev={data.prev?.tS ?? null}
            color={zone?.color ?? 'var(--ink-2)'}
            marks={[20, 35, 70, 80]}
            zones={[
              { from: 0, to: 20, color: 'var(--t-cold-2)' },
              { from: 20, to: 35, color: 'var(--t-cold-1)' },
              { from: 35, to: 70, color: 'var(--t-neutral)' },
              { from: 70, to: 80, color: 'var(--t-hot-1)' },
              { from: 80, to: 100, color: 'var(--t-hot-2)' },
            ]}
          />

          <p className="mt-3 mb-0 text-center text-xs leading-relaxed text-[var(--ink-2)]">
            ≤20 深度冰点 · ≥80 过热。
            {d.nLow > 0 && <span className="val-cold font-medium"> 当前 {d.nLow} 个维度处于冰点区。</span>}
            {d.nHigh > 0 && <span className="val-hot font-medium"> 当前 {d.nHigh} 个维度处于过热区。</span>}
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

          <GaugeMeter
            value={d.v}
            prev={data.prev?.v ?? null}
            color={tierColor}
            marks={[p.cheapBelow, p.expensiveAbove]}
            zones={[
              { from: 0, to: p.cheapBelow, color: 'var(--t-cold-2)' },
              { from: p.cheapBelow, to: p.expensiveAbove, color: 'var(--t-neutral)' },
              { from: p.expensiveAbove, to: 100, color: 'var(--t-hot-2)' },
            ]}
          />

          <p className="mt-3 mb-0 text-center text-xs leading-relaxed text-[var(--ink-2)]">
            股债利差 ERP{' '}
            <strong className="font-semibold text-[var(--ink)] mono">
              {fmt(d.erp === null ? null : d.erp * 100, 2, '%')}
            </strong>
            （1/PE − 10Y国债），近 10 年分位取反：V ≤ {p.cheapBelow} 便宜 · ≥ {p.expensiveAbove} 昂贵。
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

      {/* 算法与生效参数白盒可视化推导链 */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <strong className="text-sm font-semibold">算法与生效参数白盒推导链</strong>
          <span className="tag tag-cold">{data.paramsName || '生效参数集'}</span>
          <span className="tag tag-muted mono">版本 {data.paramsVersion}</span>
          <span className={`tag ${p.skipTPct ? 'tag-ok' : 'tag-muted'}`}>
            {p.skipTPct ? '方案 C：跳过二次分位' : '二次分位开启'}
          </span>
          <span className={`tag ${(p.signal as Record<string, unknown> | undefined)?.topOnlyExpensive ? 'tag-ok' : 'tag-muted'}`}>
            {(p.signal as Record<string, unknown> | undefined)?.topOnlyExpensive ? '仅估值昂贵时卖顶' : '全估值档卖顶'}
          </span>
          <a href="#/backtest" className="link ml-auto text-xs">
            去回测页调参与消融诊断 →
          </a>
        </div>

        <div className="grid grid-cols-1 items-stretch gap-3 p-5 lg:grid-cols-[1fr_20px_1fr_20px_1fr]">
          {/* Step 1: 估值层 V */}
          <div className="flex flex-col justify-between rounded-xl border border-[var(--line)] bg-[var(--bg-subtle)] p-4">
            <div>
              <div className="flex items-center justify-between text-xs font-semibold text-[var(--ink)]">
                <span>① 估值层 V 推导</span>
                <span className="mono text-[11px] font-normal text-[var(--ink-3)]">{d.date}</span>
              </div>
              <div className="mt-2.5 rounded-lg border border-[var(--line)] bg-[var(--panel)] px-3 py-2 mono text-[11.5px] text-[var(--ink-2)]">
                <div>ERP = 1/PE(T-1) − Y10(T-1)</div>
                {data.raw?.pe != null && data.raw?.bondY10 != null && (
                  <div className="mt-0.5 text-[var(--ink)]">
                    = 1/{data.raw.pe.toFixed(2)} − {data.raw.bondY10.toFixed(2)}% ={' '}
                    <strong className="text-[var(--accent)]">{fmt(d.erp === null ? null : d.erp * 100, 2, '%')}</strong>
                  </div>
                )}
              </div>
            </div>
            <div className="mt-3 space-y-1.5 text-xs text-[var(--ink-2)]">
              <div className="flex items-center justify-between">
                <span>近 {String(p.valuationWindow ?? 2520)} 日分位取反</span>
                <strong className="mono text-sm" style={{ color: tierColor }}>
                  V = {fmt(d.v)} ({TIER_TEXT[d.tier] ?? '—'})
                </strong>
              </div>
              <div className="relative h-2 w-full overflow-hidden rounded-full bg-[var(--panel)]">
                <div
                  className="absolute inset-y-0 left-0 bg-[var(--t-cold-bg)]"
                  style={{ width: `${p.cheapBelow}%` }}
                />
                <div
                  className="absolute inset-y-0 right-0 bg-[var(--t-hot-bg)]"
                  style={{ width: `${100 - p.expensiveAbove}%` }}
                />
                {d.v !== null && (
                  <div
                    className="absolute top-1/2 h-3 w-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full"
                    style={{ left: `${Math.min(100, Math.max(0, d.v))}%`, background: tierColor }}
                  />
                )}
              </div>
              <div className="flex justify-between text-[10.5px] text-[var(--ink-3)] mono">
                <span>便宜 ≤{p.cheapBelow}</span>
                <span>滞回 ±{String(p.tierHysteresis ?? 5)}</span>
                <span>昂贵 ≥{p.expensiveAbove}</span>
              </div>
            </div>
          </div>

          <div className="hidden items-center justify-center text-[var(--ink-3)] lg:flex" aria-hidden>
            <ArrowRight className="h-4 w-4" />
          </div>

          {/* Step 2: 择时层 T_s */}
          <div className="flex flex-col justify-between rounded-xl border border-[var(--line)] bg-[var(--bg-subtle)] p-4">
            <div>
              <div className="flex items-center justify-between text-xs font-semibold text-[var(--ink)]">
                <span>② 择时层 T_s 推导</span>
                <span className="mono text-[11px] font-normal text-[var(--ink-3)]">窗口 {String(p.timingWindow ?? 750)} 日</span>
              </div>
              <div className="mt-2.5 grid grid-cols-4 gap-1.5">
                {dimList.map((dm) => {
                  const z = zoneOf(dm.score)
                  return (
                    <div
                      key={dm.code}
                      className={`rounded-lg border border-[var(--line)] bg-[var(--panel)] px-2 py-1.5 text-center ${dm.enabled ? '' : 'opacity-45'}`}
                      title={`${dm.name}（权重 ${dm.weight}）：${dm.raw}`}
                    >
                      <div className="text-[10px] text-[var(--ink-3)] mono">{dm.code}×{dm.weight}</div>
                      <div className="mt-0.5 text-sm font-bold mono" style={{ color: dm.enabled ? z?.color : 'var(--ink-3)' }}>
                        {dm.enabled ? fmt(dm.score, 0) : '关'}
                      </div>
                    </div>
                  )
                })}
              </div>
            </div>
            <div className="mt-3 space-y-1 text-xs text-[var(--ink-2)]">
              <div className="flex items-center justify-between mono">
                <span>加权均分 T_base</span>
                <strong className="text-[var(--ink)]">{fmt(d.tBase)}</strong>
              </div>
              <div className="flex items-center justify-between">
                <span>
                  {p.skipTPct ? `方案 C 直通 → EMA(${String(p.emaSpan ?? 3)})` : `二次分位 ${fmt(d.tPct)} → EMA(${String(p.emaSpan ?? 3)})`}
                </span>
                <strong className="mono text-sm" style={{ color: zone?.color }}>
                  T_s = {fmt(d.tS)}
                </strong>
              </div>
            </div>
          </div>

          <div className="hidden items-center justify-center text-[var(--ink-3)] lg:flex" aria-hidden>
            <ArrowRight className="h-4 w-4" />
          </div>

          {/* Step 3: 分层目标仓位 */}
          <div className="flex flex-col justify-between rounded-xl border border-[var(--line)] bg-[var(--bg-subtle)] p-4">
            <div>
              <div className="flex items-center justify-between text-xs font-semibold text-[var(--ink)]">
                <span>③ 分层目标仓位</span>
                <span className="mono text-sm font-bold text-[var(--accent)]">
                  {data.position ? `${posFinalPct}%` : '—'}
                </span>
              </div>
              {data.position ? (
                <div className="mt-3 space-y-2">
                  {/* 0-100% 堆叠仓位条 */}
                  <div className="flex h-3 w-full overflow-hidden rounded-full bg-[var(--panel)] border border-[var(--line)]">
                    <div
                      className="h-full transition-all duration-300"
                      style={{
                        width: `${Math.min(posFinalPct, posBasePct)}%`,
                        background: tierColor,
                      }}
                      title={`估值底仓 ${posBasePct}%`}
                    />
                    {posOverlayPct > 0 && (
                      <div
                        className="h-full transition-all duration-300"
                        style={{
                          width: `${Math.max(0, posFinalPct - posBasePct)}%`,
                          background: 'var(--accent)',
                        }}
                        title={`底部信号加仓 +${posOverlayPct}%`}
                      />
                    )}
                  </div>
                  <div className="flex flex-wrap items-center justify-between gap-1 text-[11.5px] mono text-[var(--ink-2)]">
                    <span>
                      底仓({TIER_TEXT[data.position.tier] ?? '合理'}) <strong className="text-[var(--ink)]">{posBasePct}%</strong>
                    </span>
                    <span>
                      信号叠加{' '}
                      <strong className={posOverlayPct > 0 ? 'text-[var(--accent)]' : posOverlayPct < 0 ? 'val-hot' : ''}>
                        {posOverlayPct >= 0 ? `+${posOverlayPct}%` : `${posOverlayPct}%`}
                      </strong>
                    </span>
                  </div>
                </div>
              ) : (
                <div className="mt-2 text-xs text-[var(--ink-3)]">暂无仓位推导数据。</div>
              )}
            </div>
            <div className="mt-3 text-[11px] leading-relaxed text-[var(--ink-3)]">
              底仓规则：便宜 {Math.round(((p.position as Record<string, number> | undefined)?.baseCheap ?? 0.8) * 100)}% / 合理{' '}
              {Math.round(((p.position as Record<string, number> | undefined)?.baseFair ?? 0.5) * 100)}% / 昂贵{' '}
              {Math.round(((p.position as Record<string, number> | undefined)?.baseExpensive ?? 0.2) * 100)}%，信号步长 ±
              {Math.round(((p.position as Record<string, number> | undefined)?.step ?? 0.2) * 100)}%
            </div>
          </div>
        </div>
      </section>

      {/* 择时层四维度拆解 */}
      <section className="panel overflow-hidden">
        <div className="flex flex-wrap items-baseline justify-between gap-2 border-b border-[var(--line)] px-5 py-3.5">
          <strong className="text-sm font-semibold">择时层各维度</strong>
          <span className="text-xs text-[var(--ink-2)]">0 = 历史最冷，100 = 历史最热（近 3 年分位）</span>
        </div>
        <div className="divide-y divide-[var(--line)]">
          {dimList.map((x) => {
            const z = zoneOf(x.score)
            return (
              <div
                key={x.name}
                className={`grid grid-cols-1 items-center gap-3 px-5 py-3.5 md:grid-cols-[240px_1fr_56px_170px] ${
                  x.enabled ? '' : 'opacity-55'
                }`}
              >
                <div>
                  <div className="flex items-center gap-1.5 text-sm font-medium text-[var(--ink)]">
                    <span className="mono text-xs text-[var(--ink-3)]">{x.code}</span>
                    {x.name}
                  </div>
                  <div className="mt-0.5 text-[11.5px] text-[var(--ink-2)]">{x.hint}</div>
                </div>

                <div>
                  {x.enabled ? (
                    <div className="relative h-2 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
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

      {/* 多宽基估值与趋势雷达（四象限气泡图 + 明细表） */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <Compass className="h-4 w-4 shrink-0 text-[var(--accent)]" />
          <strong className="text-sm font-semibold">多宽基估值与趋势雷达</strong>
          <span className="text-xs text-[var(--ink-2)]">
            中证官网实时（近 10 年 PE 分位{radar?.bondY10 != null ? ` · 10Y国债 ${radar.bondY10.toFixed(2)}%` : ''}）
          </span>
          {radar?.trSpread5y != null && (
            <span className="tag tag-ok mono" title="沪深300全收益(H00300) − 沪深300价格指数 近5年年化分红再投差">
              300 红利复利 +{((radar.trSpread5y ?? 0) * 100).toFixed(2)}%/年
            </span>
          )}
          <button className="btn btn-ghost ml-auto" disabled={radarBusy} onClick={() => loadRadar(true)}>
            <RefreshCw className={`h-3.5 w-3.5 ${radarBusy ? 'animate-spin' : ''}`} />
            刷新
          </button>
        </div>

        {radar ? (
          <>
            {radarOption && (
              <div className="border-b border-[var(--line)] bg-[var(--bg-subtle)]/40 px-4 pt-3 pb-2">
                <div className="mb-1 flex flex-wrap items-center justify-between gap-2 px-1 text-[11.5px] text-[var(--ink-2)]">
                  <span>四象限估值-趋势气泡图（气泡大小 = 股债利差 ERP，左蓝低估 · 右红高估 · 上强下弱）</span>
                  <span className="mono text-[var(--ink-3)]">悬停气泡查看 PE / ERP / 60日动量</span>
                </div>
                <EChart option={radarOption} height={250} />
              </div>
            )}

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
                    const pct5 = x.pePct5Y
                    const pct10 = x.pePct10Y
                    const pctColor = (v: Num) =>
                      v === null ? 'var(--ink-2)' : v <= 30 ? 'var(--t-cold-2)' : v >= 70 ? 'var(--t-hot-2)' : 'var(--accent)'
                    const pFmt = (v: Num) => (v === null ? '—' : `${v >= 0 ? '+' : ''}${(v * 100).toFixed(1)}%`)
                    return (
                      <tr key={x.code}>
                        <td className="nowrap">
                          <div className="font-medium text-[var(--ink)]">{x.name}</div>
                          <div className="mono text-[11px] text-[var(--ink-3)]">
                            {x.code} {x.date && `· ${x.date}`}
                          </div>
                        </td>
                        <td className="nowrap">
                          <span className={`tag ${x.isTR ? 'tag-ok' : 'tag-muted'}`}>{x.style}</span>
                        </td>
                        {x.error ? (
                          <td colSpan={7} className="muted small">
                            拉取失败：{x.error}
                          </td>
                        ) : (
                          <>
                            <td className="num mono nowrap">{x.close.toFixed(2)}</td>
                            <td className="num mono nowrap">{x.pe !== null ? x.pe.toFixed(2) : '—'}</td>
                            <td className="num mono nowrap">{x.erp !== null ? `${x.erp.toFixed(2)}%` : '—'}</td>
                            <td className="num mono small nowrap">
                              <div className="ml-auto flex w-28 flex-col gap-1">
                                <div className="flex items-center justify-between text-[11px]">
                                  <span className="text-[var(--ink-3)]">5Y {pct5 !== null ? `${pct5.toFixed(0)}%` : '—'}</span>
                                  <strong style={{ color: pctColor(pct10) }}>10Y {pct10 !== null ? `${pct10.toFixed(0)}%` : '—'}</strong>
                                </div>
                                <div className="flex flex-col gap-0.5">
                                  {pct5 !== null && (
                                    <div className="h-1 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]" title={`5年 PE 分位 ${pct5.toFixed(1)}%`}>
                                      <div className="h-full rounded-full opacity-55" style={{ width: `${pct5}%`, background: pctColor(pct5) }} />
                                    </div>
                                  )}
                                  {pct10 !== null && (
                                    <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]" title={`10年 PE 分位 ${pct10.toFixed(1)}%`}>
                                      <div className="h-full rounded-full" style={{ width: `${pct10}%`, background: pctColor(pct10) }} />
                                    </div>
                                  )}
                                </div>
                              </div>
                            </td>
                            <td className="num mono small nowrap">
                              <span className={(x.ret20d ?? 0) >= 0 ? 'up' : 'down'}>{pFmt(x.ret20d)}</span>
                              {' / '}
                              <span className={(x.ret60d ?? 0) >= 0 ? 'up' : 'down'}>{pFmt(x.ret60d)}</span>
                              {' / '}
                              <span className={(x.ret250d ?? 0) >= 0 ? 'up' : 'down'}>{pFmt(x.ret250d)}</span>
                            </td>
                            <td className={`num mono small nowrap ${(x.ma200Dist ?? 0) >= 0 ? 'up' : 'down'}`}>{pFmt(x.ma200Dist)}</td>
                            <td className="nowrap">
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
          </>
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
        温度与估值档仅描述市场相对历史的位置，所有参数与计算步骤均已在上方白盒面板完整呈现；可在「策略回测」页自由调整并对比消融结果，不构成投资建议。
      </p>
    </div>
  )
}
