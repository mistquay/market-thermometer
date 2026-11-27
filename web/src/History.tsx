import { useEffect, useMemo, useState } from 'react'
import { Activity, ArrowDownRight, ArrowUpRight, Flame, Snowflake, TrendingUp } from 'lucide-react'
import { api, type ScoreSeries } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { pct } from './temperature'
import { useTheme } from './theme'

const RANGES = [
  { key: '1y', label: '1 年', years: 1 },
  { key: '3y', label: '3 年', years: 3 },
  { key: '5y', label: '5 年', years: 5 },
  { key: 'all', label: '全部', years: 0 },
] as const

const DIMS = [
  { key: 't1', label: '资金热度', varName: '--dim-1' },
  { key: 't2', label: '杠杆情绪', varName: '--dim-2' },
  { key: 't3', label: '市场广度', varName: '--t-cold-1' },
  { key: 't4', label: '价格动量', varName: '--dim-4' },
] as const

type DimKey = (typeof DIMS)[number]['key']

export default function History() {
  const [data, setData] = useState<ScoreSeries | null>(null)
  const [error, setError] = useState('')
  const [range, setRange] = useState<(typeof RANGES)[number]['key']>('5y')
  const [activeDims, setActiveDims] = useState<Record<DimKey, boolean>>({
    t1: false,
    t2: false,
    t3: false,
    t4: false,
  })
  const { theme } = useTheme()

  useEffect(() => {
    api.scores().then(setData, (e) => setError(e.message))
  }, [])

  const firstUseful = useMemo(() => {
    if (!data) return 0
    const i = data.series.tS.findIndex((x) => x !== null)
    return i < 0 ? 0 : i
  }, [data])

  const sliceInfo = useMemo(() => {
    if (!data || data.dates.length === 0) return null
    const dates = data.dates.slice(firstUseful)
    const r = RANGES.find((x) => x.key === range)!
    let startIdx = 0
    if (r.years > 0) {
      const d = new Date(dates[dates.length - 1])
      d.setFullYear(d.getFullYear() - r.years)
      const iso = d.toISOString().slice(0, 10)
      const idx = dates.findIndex((x) => x >= iso)
      startIdx = idx < 0 ? 0 : idx
    }
    const closes = data.series.close.slice(firstUseful + startIdx).filter((x): x is number => x !== null)
    const tss = data.series.tS.slice(firstUseful + startIdx).filter((x): x is number => x !== null)
    const vs = data.series.v.slice(firstUseful + startIdx).filter((x): x is number => x !== null)
    const th = data.thresholds

    const cFirst = closes[0] ?? null
    const cLast = closes[closes.length - 1] ?? null
    const cRet = cFirst && cLast ? cLast / cFirst - 1 : null
    const cHigh = closes.length ? Math.max(...closes) : null
    const cLow = closes.length ? Math.min(...closes) : null

    const tsLast = tss[tss.length - 1] ?? null
    const tsAvg = tss.length ? tss.reduce((a, b) => a + b, 0) / tss.length : null
    const coldDays = tss.filter((x) => x <= th.extreme).length
    const hotDays = tss.filter((x) => x >= 100 - th.extreme).length

    const vLast = vs[vs.length - 1] ?? null
    const cheapDays = vs.filter((x) => x <= th.cheap).length
    const expDays = vs.filter((x) => x >= th.expensive).length

    return {
      startValue: dates[startIdx] ?? dates[0],
      fromDate: dates[startIdx] ?? dates[0],
      toDate: dates[dates.length - 1],
      days: dates.length - startIdx,
      cLast,
      cRet,
      cHigh,
      cLow,
      tsLast,
      tsAvg,
      coldPct: tss.length ? coldDays / tss.length : 0,
      hotPct: tss.length ? hotDays / tss.length : 0,
      coldDays,
      hotDays,
      vLast,
      cheapPct: vs.length ? cheapDays / vs.length : 0,
      expPct: vs.length ? expDays / vs.length : 0,
    }
  }, [data, firstUseful, range])

  const option = useMemo<EChartsOption | null>(() => {
    if (!data || data.dates.length === 0 || !sliceInfo) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const accent = cssVar('--accent')
    const cold = cssVar('--t-cold-bg')
    const hot = cssVar('--t-hot-bg')
    const th = data.thresholds
    const dates = data.dates.slice(firstUseful)
    const col = (k: keyof ScoreSeries['series']) => data.series[k].slice(firstUseful)

    const grids = [
      { left: 58, right: 24, top: 32, height: '29%' },
      { left: 58, right: 24, top: '40%', height: '25%' },
      { left: 58, right: 24, top: '70%', height: '19%' },
    ]
    const axis = (i: number) => ({
      type: 'category' as const,
      gridIndex: i,
      data: dates,
      boundaryGap: false,
      axisLine: { lineStyle: { color: line } },
      axisLabel: { color: ink, fontSize: 11, show: i === 2 },
      axisTick: { show: i === 2 },
    })
    const yAxis = (i: number, opts: object) => ({
      type: 'value' as const,
      gridIndex: i,
      splitLine: { lineStyle: { color: line, type: 'dashed' } },
      axisLabel: { color: ink, fontSize: 11 },
      ...opts,
    })
    const band = (lo: number, hi: number) => ({
      silent: true,
      data: [
        [{ yAxis: 0, itemStyle: { color: cold } }, { yAxis: lo }],
        [{ yAxis: hi, itemStyle: { color: hot } }, { yAxis: 100 }],
      ],
    })

    return {
      animation: false,
      axisPointer: { link: [{ xAxisIndex: 'all' }] },
      tooltip: {
        trigger: 'axis',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        axisPointer: { type: 'line', lineStyle: { color: cssVar('--line-strong'), type: 'dashed' } },
      },
      legend: { top: 0, right: 24, textStyle: { color: ink, fontSize: 12 }, itemWidth: 14, itemHeight: 3 },
      grid: grids,
      xAxis: [axis(0), axis(1), axis(2)],
      yAxis: [
        yAxis(0, { scale: true, name: '沪深300', nameTextStyle: { color: ink, fontSize: 11 } }),
        yAxis(1, { min: 0, max: 100, interval: 20, name: '温度 T_s', nameTextStyle: { color: ink, fontSize: 11 } }),
        yAxis(2, { min: 0, max: 100, interval: 25, name: '估值 V', nameTextStyle: { color: ink, fontSize: 11 } }),
      ],
      dataZoom: [
        { type: 'inside', xAxisIndex: [0, 1, 2], startValue: sliceInfo.startValue },
        {
          type: 'slider',
          xAxisIndex: [0, 1, 2],
          startValue: sliceInfo.startValue,
          bottom: 4,
          height: 18,
          borderColor: cssVar('--line'),
          textStyle: { color: ink },
        },
      ],
      series: [
        {
          name: '沪深300',
          type: 'line',
          xAxisIndex: 0,
          yAxisIndex: 0,
          showSymbol: false,
          data: col('close'),
          lineStyle: { width: 1.6, color: inkMain },
          itemStyle: { color: inkMain },
          areaStyle: {
            color: {
              type: 'linear',
              x: 0,
              y: 0,
              x2: 0,
              y2: 1,
              colorStops: [
                { offset: 0, color: theme === 'dark' ? 'rgba(241, 245, 249, 0.10)' : 'rgba(15, 23, 42, 0.08)' },
                { offset: 1, color: 'rgba(241, 245, 249, 0.00)' },
              ],
            },
          },
        },
        {
          name: '择时温度 T_s',
          type: 'line',
          xAxisIndex: 1,
          yAxisIndex: 1,
          showSymbol: false,
          data: col('tS'),
          lineStyle: { width: 2, color: accent },
          itemStyle: { color: accent },
          markArea: band(th.extreme, 100 - th.extreme),
        },
        ...DIMS.filter((d) => activeDims[d.key]).map((d) => ({
          name: d.label,
          type: 'line' as const,
          xAxisIndex: 1,
          yAxisIndex: 1,
          showSymbol: false,
          data: col(d.key),
          lineStyle: { width: 1.2, color: cssVar(d.varName), opacity: 0.85 },
          itemStyle: { color: cssVar(d.varName) },
        })),
        {
          name: '估值 V',
          type: 'line',
          xAxisIndex: 2,
          yAxisIndex: 2,
          showSymbol: false,
          data: col('v'),
          lineStyle: { width: 1.8, color: cssVar('--t-hot-1') },
          itemStyle: { color: cssVar('--t-hot-1') },
          markArea: band(th.cheap, th.expensive),
          markLine: {
            silent: true,
            symbol: 'none',
            label: { color: ink, fontSize: 10, position: 'insideEndTop' },
            lineStyle: { type: 'dashed', width: 1, opacity: 0.65 },
            data: [
              { yAxis: th.cheap, lineStyle: { color: cssVar('--t-cold-2') }, label: { formatter: `便宜 ≤${th.cheap}` } },
              { yAxis: th.expensive, lineStyle: { color: cssVar('--t-hot-2') }, label: { formatter: `昂贵 ≥${th.expensive}` } },
            ],
          },
        },
      ],
    }
  }, [data, firstUseful, sliceInfo, activeDims, theme])

  const toggleDim = (k: DimKey) => setActiveDims((prev) => ({ ...prev, [k]: !prev[k] }))

  if (error) return <div className="error" role="alert">{error}</div>

  return (
    <div className="page">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="m-0">历史走势</h1>
          <p className="sub mt-1">
            沪深300、择时温度 T_s 与估值 V 三图联动缩放。蓝区代表冰点 / 便宜，红区代表过热 / 昂贵。
          </p>
        </div>
        <div className="seg" role="group" aria-label="时间范围">
          {RANGES.map((r) => (
            <button
              key={r.key}
              className={r.key === range ? 'on' : ''}
              aria-pressed={r.key === range}
              onClick={() => setRange(r.key)}
            >
              {r.label}
            </button>
          ))}
        </div>
      </div>

      {/* 区间动态 KPI 概览条 */}
      {sliceInfo && (
        <div className="grid grid-cols-2 gap-3.5 xl:grid-cols-4">
          <div className="panel p-4">
            <div className="flex items-center justify-between text-xs text-[var(--ink-2)]">
              <span>沪深300 区间表现</span>
              <TrendingUp className="h-3.5 w-3.5 text-[var(--ink-3)]" />
            </div>
            <div className="mt-1.5 flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight mono">
                {sliceInfo.cLast?.toLocaleString('zh-CN', { maximumFractionDigits: 1 }) ?? '—'}
              </span>
              {sliceInfo.cRet !== null && (
                <span className={`inline-flex items-center text-xs font-semibold mono ${sliceInfo.cRet >= 0 ? 'up' : 'down'}`}>
                  {sliceInfo.cRet >= 0 ? <ArrowUpRight className="h-3.5 w-3.5" /> : <ArrowDownRight className="h-3.5 w-3.5" />}
                  {pct(sliceInfo.cRet)}
                </span>
              )}
            </div>
            <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
              区间 {sliceInfo.cLow?.toFixed(0)} ~ {sliceInfo.cHigh?.toFixed(0)}
            </div>
          </div>

          <div className="panel p-4">
            <div className="flex items-center justify-between text-xs text-[var(--ink-2)]">
              <span>择时温度 T_s</span>
              <Activity className="h-3.5 w-3.5 text-[var(--accent)]" />
            </div>
            <div className="mt-1.5 flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight mono text-[var(--accent)]">
                {sliceInfo.tsLast !== null ? sliceInfo.tsLast.toFixed(1) : '—'}
              </span>
              <span className="text-xs text-[var(--ink-2)] mono">
                均值 {sliceInfo.tsAvg !== null ? sliceInfo.tsAvg.toFixed(1) : '—'}
              </span>
            </div>
            <div className="mt-1 flex items-center gap-3 text-[11.5px] mono">
              <span className="inline-flex items-center gap-1 text-[var(--t-cold-2)]">
                <Snowflake className="h-3 w-3" />
                冰点 {(sliceInfo.coldPct * 100).toFixed(0)}% ({sliceInfo.coldDays}日)
              </span>
              <span className="inline-flex items-center gap-1 text-[var(--t-hot-2)]">
                <Flame className="h-3 w-3" />
                过热 {(sliceInfo.hotPct * 100).toFixed(0)}% ({sliceInfo.hotDays}日)
              </span>
            </div>
          </div>

          <div className="panel p-4">
            <div className="flex items-center justify-between text-xs text-[var(--ink-2)]">
              <span>估值背景 V</span>
              <span className="text-[11px] text-[var(--ink-3)]">近 10 年分位</span>
            </div>
            <div className="mt-1.5 flex items-baseline gap-2">
              <span className="text-2xl font-bold tracking-tight mono text-[var(--t-hot-1)]">
                {sliceInfo.vLast !== null ? sliceInfo.vLast.toFixed(1) : '—'}
              </span>
            </div>
            <div className="mt-1 flex items-center gap-3 text-[11.5px] text-[var(--ink-2)] mono">
              <span>便宜区 {(sliceInfo.cheapPct * 100).toFixed(0)}%</span>
              <span>·</span>
              <span>昂贵区 {(sliceInfo.expPct * 100).toFixed(0)}%</span>
            </div>
          </div>

          <div className="panel p-4">
            <div className="text-xs text-[var(--ink-2)]">统计窗口</div>
            <div className="mt-1.5 text-lg font-semibold tracking-tight mono">
              {sliceInfo.days.toLocaleString()} <span className="text-xs font-normal text-[var(--ink-2)]">个交易日</span>
            </div>
            <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
              {sliceInfo.fromDate} ~ {sliceInfo.toDate}
            </div>
          </div>
        </div>
      )}

      {/* 图表主卡 + 维度叠加胶囊 */}
      <section className="panel overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs font-medium text-[var(--ink-2)]">叠加子维度：</span>
            {DIMS.map((d) => {
              const on = activeDims[d.key]
              const hasData = !!data && data.series[d.key].some((v) => v !== null)
              return (
                <button
                  key={d.key}
                  type="button"
                  aria-pressed={on}
                  disabled={!hasData}
                  title={hasData ? `叠加/隐藏${d.label}历史曲线` : '当前数据集中该维度暂无历史序列'}
                  onClick={() => toggleDim(d.key)}
                  className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors ${
                    !hasData
                      ? 'cursor-not-allowed border-[var(--line)] opacity-45'
                      : on
                        ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--ink)]'
                        : 'border-[var(--line)] bg-transparent text-[var(--ink-2)] hover:border-[var(--line-strong)] hover:text-[var(--ink)]'
                  }`}
                >
                  <span
                    className="h-2 w-2 rounded-full"
                    style={{ background: `var(${d.varName})`, opacity: on ? 1 : 0.4 }}
                  />
                  {d.label}
                  {!hasData && <span className="text-[10px] text-[var(--ink-3)]">(无数据)</span>}
                </button>
              )
            })}
          </div>
          <span className="text-xs text-[var(--ink-3)]">滚轮缩放 · 拖拽底部滑块</span>
        </div>

        <div className="p-3">
          {option ? (
            <EChart option={option} height={600} />
          ) : (
            data && <div className="empty">还没有打分结果，先到「数据同步」页执行同步。</div>
          )}
        </div>
      </section>
    </div>
  )
}
