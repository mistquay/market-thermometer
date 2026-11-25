import { useEffect, useMemo, useState } from 'react'
import { api, type ScoreSeries } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'

const RANGES = [
  { key: '1y', label: '1 年', years: 1 },
  { key: '3y', label: '3 年', years: 3 },
  { key: '5y', label: '5 年', years: 5 },
  { key: 'all', label: '全部', years: 0 },
] as const

const DIMS = [
  { key: 't1', label: '资金热度' },
  { key: 't2', label: '杠杆情绪' },
  { key: 't3', label: '市场广度' },
  { key: 't4', label: '价格动量' },
] as const

export default function History() {
  const [data, setData] = useState<ScoreSeries | null>(null)
  const [error, setError] = useState('')
  const [range, setRange] = useState<(typeof RANGES)[number]['key']>('5y')
  const [showDims, setShowDims] = useState(false)

  useEffect(() => {
    api.scores().then(setData, (e) => setError(e.message))
  }, [])

  // 只展示 T_s 或 V 已经有值的区间，跳过预热期
  const firstUseful = useMemo(() => {
    if (!data) return 0
    const i = data.series.tS.findIndex((x) => x !== null)
    return i < 0 ? 0 : i
  }, [data])

  const option = useMemo<EChartsOption | null>(() => {
    if (!data || data.dates.length === 0) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line'), accent = cssVar('--accent')
    const cold = cssVar('--t-cold-bg'), hot = cssVar('--t-hot-bg')
    const th = data.thresholds
    const dates = data.dates.slice(firstUseful)
    const col = (k: keyof ScoreSeries['series']) => data.series[k].slice(firstUseful)

    const r = RANGES.find((x) => x.key === range)!
    let startValue = dates[0]
    if (r.years > 0) {
      const d = new Date(dates[dates.length - 1])
      d.setFullYear(d.getFullYear() - r.years)
      const iso = d.toISOString().slice(0, 10)
      startValue = dates.find((x) => x >= iso) ?? dates[0]
    }

    const grids = [
      { left: 64, right: 24, top: 28, height: '30%' },
      { left: 64, right: 24, top: '44%', height: '24%' },
      { left: 64, right: 24, top: '74%', height: '14%' },
    ]
    const axis = (i: number) => ({
      type: 'category' as const, gridIndex: i, data: dates, boundaryGap: false,
      axisLine: { lineStyle: { color: line } },
      axisLabel: { color: ink, show: i === 2 },
      axisTick: { show: i === 2 },
    })
    const yAxis = (i: number, opts: object) => ({
      type: 'value' as const, gridIndex: i, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink }, ...opts,
    })
    const band = (lo: number, hi: number) => ({
      silent: true,
      data: [
        [{ yAxis: 0, itemStyle: { color: cold } }, { yAxis: lo }],
        [{ yAxis: hi, itemStyle: { color: hot } }, { yAxis: 100 }],
      ],
    })

    const dimColors = [cssVar('--dim-1'), cssVar('--dim-2'), cssVar('--t-cold-1'), cssVar('--dim-4')]
    return {
      animation: false,
      axisPointer: { link: [{ xAxisIndex: 'all' }] },
      tooltip: { trigger: 'axis', axisPointer: { type: 'line' } },
      legend: { top: 0, right: 24, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
      grid: grids,
      xAxis: [axis(0), axis(1), axis(2)],
      yAxis: [
        yAxis(0, { scale: true, name: '收盘', nameTextStyle: { color: ink } }),
        yAxis(1, { min: 0, max: 100, interval: 20, name: '温度', nameTextStyle: { color: ink } }),
        yAxis(2, { min: 0, max: 100, interval: 50, name: '估值', nameTextStyle: { color: ink } }),
      ],
      dataZoom: [
        { type: 'inside', xAxisIndex: [0, 1, 2], startValue },
        { type: 'slider', xAxisIndex: [0, 1, 2], startValue, bottom: 8, height: 20, borderColor: line, textStyle: { color: ink } },
      ],
      series: [
        {
          name: '沪深300', type: 'line', xAxisIndex: 0, yAxisIndex: 0, showSymbol: false, data: col('close'),
          lineStyle: { width: 1.2, color: ink }, itemStyle: { color: ink },
        },
        {
          name: '择时温度 T_s', type: 'line', xAxisIndex: 1, yAxisIndex: 1, showSymbol: false, data: col('tS'),
          lineStyle: { width: 1.5, color: accent }, itemStyle: { color: accent },
          markArea: band(th.extreme, 100 - th.extreme),
        },
        ...(showDims
          ? DIMS.map((d, i) => ({
              name: d.label, type: 'line', xAxisIndex: 1, yAxisIndex: 1, showSymbol: false, data: col(d.key),
              lineStyle: { width: 1, color: dimColors[i], opacity: 0.8 }, itemStyle: { color: dimColors[i] },
            }))
          : []),
        {
          name: '估值 V', type: 'line', xAxisIndex: 2, yAxisIndex: 2, showSymbol: false, data: col('v'),
          lineStyle: { width: 1.5, color: cssVar('--t-hot-1') }, itemStyle: { color: cssVar('--t-hot-1') },
          markArea: band(th.cheap, th.expensive),
        },
      ],
    }
  }, [data, firstUseful, range, showDims])

  if (error) return <div className="error" role="alert">{error}</div>

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>历史</h1>
          <p className="sub">
            沪深300、择时温度 T_s 与估值 V 联动，拖动下方滑块或滚轮缩放。蓝底为冰点 / 便宜区，红底为过热 / 昂贵区。
          </p>
        </div>
        <div className="actions">
          <label className="check">
            <input type="checkbox" checked={showDims} onChange={(e) => setShowDims(e.target.checked)} />
            显示各维度
          </label>
          <div className="seg" role="group" aria-label="时间范围">
            {RANGES.map((r) => (
              <button key={r.key} className={r.key === range ? 'on' : ''} aria-pressed={r.key === range} onClick={() => setRange(r.key)}>
                {r.label}
              </button>
            ))}
          </div>
        </div>
      </div>
      <section className="panel chart-panel">
        {option ? <EChart option={option} height={620} /> : data && <div className="empty">还没有打分结果，先到"数据"页同步。</div>}
      </section>
    </div>
  )
}
