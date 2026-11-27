// TradingView Lightweight Charts 渲染器（Apache-2.0）。按许可要求保留图上的 TradingView 归属标志
// （库的类型说明：保留 attributionLogo 即满足链接要求）。
import { useEffect, useRef, useState } from 'react'
import {
  CandlestickSeries, ColorType, CrosshairMode, HistogramSeries, LineSeries, createChart, createSeriesMarkers,
  type IChartApi, type MouseEventParams, type SeriesMarker, type Time,
} from 'lightweight-charts'
import { cssVar } from '../EChart'
import { useTheme } from '../theme'
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
  const { theme } = useTheme()

  useEffect(() => {
    const el = box.current
    if (!el || data.dates.length === 0) return
    const ink = cssVar('--ink-2'), line = cssVar('--chart-grid') || cssVar('--line')
    const chart: IChartApi = createChart(el, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: 'transparent' },
        textColor: ink,
        fontSize: 12,
        panes: { separatorColor: line, separatorHoverColor: line },
      },
      grid: { vertLines: { color: line }, horzLines: { color: line } },
      crosshair: { mode: CrosshairMode.Normal },
      rightPriceScale: { borderColor: line, scaleMargins: { top: 0.1, bottom: 0.08 } },
      timeScale: { borderColor: line, rightOffset: 5 },
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
    const mainSeries = candle
      ? chart.addSeries(CandlestickSeries, {
          upColor: UP, downColor: DOWN, borderUpColor: UP, borderDownColor: DOWN, wickUpColor: UP, wickDownColor: DOWN,
          priceLineVisible: false, priceFormat,
        }, 0)
      : chart.addSeries(LineSeries, { color: cssVar('--accent'), lineWidth: 2, priceLineVisible: false, priceFormat }, 0)

    if (candle) {
      mainSeries.setData(data.dates.map((_, i) => {
        const o = open[i], h = high[i], l = low[i]
        return o === null || h === null || l === null ? { time: t(i) } : { time: t(i), open: o, high: h, low: l, close: data.close[i] }
      }))
    } else {
      mainSeries.setData(data.dates.map((_, i) => ({ time: t(i), value: data.close[i] })))
    }

    // 叠加模拟盘买卖点标记（Lightweight Charts v5 createSeriesMarkers）
    if (data.markers && data.markers.length > 0 && !times) {
      const dateSet = new Set(data.dates)
      const ms: SeriesMarker<Time>[] = data.markers
        .filter((m) => dateSet.has(m.date))
        .slice()
        .sort((a, b) => a.date.localeCompare(b.date))
        .map((m) => ({
          time: m.date as Time,
          position: m.side === 'buy' ? 'belowBar' : 'aboveBar',
          color: m.side === 'buy' ? UP : DOWN,
          shape: m.side === 'buy' ? 'arrowUp' : 'arrowDown',
          text: m.text,
          size: 1.2,
        }))
      if (ms.length > 0) createSeriesMarkers(mainSeries, ms)
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
    chart.timeScale().setVisibleLogicalRange({ from: Math.max(0, n - 250), to: n + 5 }) // 默认最近约一年
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
  }, [data, theme])

  const barH = 30
  return (
    <div className="flex flex-col" style={{ height }}>
      {/* 独立顶部 OHLCV 信息条，避免遮挡 K 线顶部价格刻度 */}
      {legend && (
        <div className="flex min-h-[30px] flex-wrap items-center gap-x-3 gap-y-1 border-b border-[var(--line)] bg-[var(--bg-subtle)]/60 px-3 py-1 text-xs mono">
          <span className="font-medium text-[var(--ink)]">{legend.date}</span>
          <span className="muted">{data.title}</span>
          {legend.o !== undefined && (
            <span className="text-[var(--ink-2)]">
              开 <strong className="text-[var(--ink)]">{fmt(legend.o, data.digits)}</strong>{' '}
              高 <strong className="up">{fmt(legend.h, data.digits)}</strong>{' '}
              低 <strong className="down">{fmt(legend.l, data.digits)}</strong>
            </span>
          )}
          <span className="text-[var(--ink-2)]">
            收 <strong className="text-[var(--ink)]">{fmt(legend.c, data.digits)}</strong>
          </span>
          {legend.lines.map((x) => (
            <span key={x.name} style={{ color: x.color }}>
              {x.name} {fmt(x.v, data.digits)}
            </span>
          ))}
          {legend.v !== undefined && <span className="muted">量 {fmtVol(legend.v, data.volUnit)}</span>}
        </div>
      )}
      <div className="relative flex-1" style={{ minHeight: Math.max(200, height - barH) }}>
        <div ref={box} className="absolute inset-0 px-2 pb-1" />
      </div>
    </div>
  )
}
