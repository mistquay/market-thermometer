// TradingView Lightweight Charts 渲染器（Apache-2.0）。按许可要求保留图上的 TradingView 归属标志
// （库的类型说明：保留 attributionLogo 即满足链接要求）。
import { useEffect, useRef, useState } from 'react'
import {
  CandlestickSeries, ColorType, CrosshairMode, HistogramSeries, LineSeries, createChart,
  type IChartApi, type MouseEventParams, type Time,
} from 'lightweight-charts'
import { cssVar } from '../EChart'
import { DOWN, UP, type KlineData, type KlineProps, type Num } from './kline'

type Legend = { date: string; o?: number; h?: number; l?: number; c?: number; v?: number; lines: { name: string; color: string; v: Num }[] }

function legendAt(d: KlineData, i: number): Legend {
  return {
    date: d.dates[i],
    o: d.open?.[i] ?? undefined, h: d.high?.[i] ?? undefined, l: d.low?.[i] ?? undefined, c: d.close[i],
    v: d.volume?.[i] ?? undefined,
    lines: d.lines.map((x) => ({ name: x.name, color: x.color, v: x.data[i] })),
  }
}

const fmt = (x: number | undefined | null, digits = 3) => (x === undefined || x === null ? '—' : x.toFixed(digits))
const fmtVol = (x: number | undefined, unit?: string) => (x === undefined ? '—'
  : unit ? `${x >= 1e4 ? x.toFixed(0) : x.toPrecision(4)} ${unit}`
  : x >= 1e8 ? `${(x / 1e8).toFixed(2)} 亿股` : `${(x / 1e4).toFixed(0)} 万股`)

export default function LwcKline({ data, height }: KlineProps) {
  const box = useRef<HTMLDivElement>(null)
  const [legend, setLegend] = useState<Legend | null>(null)

  useEffect(() => {
    const el = box.current
    if (!el || data.dates.length === 0) return
    const ink = cssVar('--ink-2'), line = cssVar('--line')
    const chart: IChartApi = createChart(el, {
      autoSize: true,
      layout: { background: { type: ColorType.Solid, color: 'transparent' }, textColor: ink, fontSize: 12,
        panes: { separatorColor: line, separatorHoverColor: line } },
      grid: { vertLines: { color: line }, horzLines: { color: line } },
      crosshair: { mode: CrosshairMode.Normal },
      rightPriceScale: { borderColor: line },
      timeScale: { borderColor: line, rightOffset: 4 },
      localization: { locale: 'zh-CN', dateFormat: 'yyyy-MM-dd' },
    })
    // 库按 UTC 显示时间戳：日内数据加上本地时区偏移，让坐标轴显示本地时间
    const tz = -new Date().getTimezoneOffset() * 60
    const { times } = data
    if (times) chart.applyOptions({ timeScale: { timeVisible: true, secondsVisible: false } })
    const t = (i: number) => (times ? times[i] + tz : data.dates[i]) as Time
    const digits = data.digits ?? 3
    const priceFormat = { type: 'price' as const, precision: digits, minMove: 10 ** -digits }
    const { open, high, low, volume } = data
    const candle = !!(open && high && low)
    if (candle) {
      const s = chart.addSeries(CandlestickSeries, {
        upColor: UP, downColor: DOWN, borderUpColor: UP, borderDownColor: DOWN, wickUpColor: UP, wickDownColor: DOWN,
        priceLineVisible: false, priceFormat,
      }, 0)
      s.setData(data.dates.map((_, i) => {
        const o = open[i], h = high[i], l = low[i]
        return o === null || h === null || l === null ? { time: t(i) } : { time: t(i), open: o, high: h, low: l, close: data.close[i] }
      }))
    } else {
      const s = chart.addSeries(LineSeries, { color: cssVar('--accent'), lineWidth: 2, priceLineVisible: false, priceFormat }, 0)
      s.setData(data.dates.map((_, i) => ({ time: t(i), value: data.close[i] })))
    }
    for (const l of data.lines) {
      const s = chart.addSeries(LineSeries, { color: l.color, lineWidth: 1, priceLineVisible: false, lastValueVisible: false, crosshairMarkerVisible: false }, 0)
      s.setData(l.data.map((v, i) => (v === null ? { time: t(i) } : { time: t(i), value: v })))
    }
    if (volume) {
      const s = chart.addSeries(HistogramSeries, { priceFormat: { type: 'volume' }, priceLineVisible: false, lastValueVisible: false }, 1)
      s.setData(volume.map((v, i) => (v === null ? { time: t(i) }
        : { time: t(i), value: v, color: data.close[i] >= (open?.[i] ?? data.close[i]) ? UP : DOWN })))
      chart.panes()[1]?.setStretchFactor(0.28)
    }
    const n = data.dates.length
    chart.timeScale().setVisibleLogicalRange({ from: Math.max(0, n - 250), to: n + 4 }) // 默认最近约一年
    setLegend(legendAt(data, n - 1))
    const index = new Map(data.dates.map((_, i) => [String(t(i)), i]))
    const onMove = (p: MouseEventParams) => {
      const i = p.time === undefined ? n - 1 : index.get(String(p.time))
      if (i !== undefined) setLegend(legendAt(data, i))
    }
    chart.subscribeCrosshairMove(onMove)
    return () => {
      chart.unsubscribeCrosshairMove(onMove)
      chart.remove()
    }
  }, [data])

  return (
    <div className="lwc-wrap" style={{ height }}>
      <div ref={box} className="lwc-box" />
      {legend && (
        <div className="lwc-legend mono">
          <span>{legend.date}</span>
          <span className="muted">{data.title}</span>
          {legend.o !== undefined && <span>开 {fmt(legend.o, data.digits)} 高 {fmt(legend.h, data.digits)} 低 {fmt(legend.l, data.digits)}</span>}
          <span>收 {fmt(legend.c, data.digits)}</span>
          {legend.lines.map((x) => <span key={x.name} style={{ color: x.color }}>{x.name} {fmt(x.v, data.digits)}</span>)}
          {legend.v !== undefined && <span className="muted">量 {fmtVol(legend.v, data.volUnit)}</span>}
        </div>
      )}
    </div>
  )
}
