// ECharts 渲染器：K 线 + 成交量双网格，或收盘价折线，支持模拟盘买卖点叠加。
import { useMemo } from 'react'
import EChart, { cssVar, type EChartsOption } from '../EChart'
import { useTheme } from '../theme'
import { DOWN, UP, type KlineProps } from './kline'

export default function EChartsKline({ data, height }: KlineProps) {
  const { theme } = useTheme()
  const option = useMemo<EChartsOption>(() => {
    const ink = cssVar('--ink-2'), inkMain = cssVar('--ink'), line = cssVar('--chart-grid') || cssVar('--line')
    const tooltipBase = {
      trigger: 'axis' as const,
      backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
      borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
      borderWidth: 1,
      textStyle: { color: inkMain, fontSize: 12 },
    }
    const lines = data.lines.map((l) => ({
      name: l.name, type: 'line', data: l.data, showSymbol: false,
      lineStyle: { width: 1.1, color: l.color }, itemStyle: { color: l.color },
    }))
    const dateIdx = new Map(data.dates.map((d, i) => [d, i]))
    const markData = (data.markers ?? [])
      .map((m) => {
        const idx = dateIdx.get(m.date)
        if (idx === undefined) return null
        const y = m.side === 'buy' ? (data.low?.[idx] ?? data.close[idx]) : (data.high?.[idx] ?? data.close[idx])
        return {
          coord: [m.date, y],
          symbol: m.side === 'buy' ? 'triangle' : 'path://M0,0 L10,0 L5,10 Z',
          symbolSize: 11,
          symbolOffset: [0, m.side === 'buy' ? 12 : -12],
          itemStyle: { color: m.side === 'buy' ? UP : DOWN },
          label: { show: true, formatter: m.text, color: m.side === 'buy' ? UP : DOWN, fontSize: 10, position: m.side === 'buy' ? 'bottom' : 'top' },
          name: m.detail || `${m.date} ${m.text}`,
        }
      })
      .filter((x): x is NonNullable<typeof x> => x !== null)

    const markPoint = markData.length
      ? { data: markData, tooltip: { formatter: (p: { name: string }) => p.name } }
      : undefined

    const { open, high, low, volume } = data
    if (open && high && low) {
      const zoom = { start: 85, end: 100 }
      const k = data.dates.map((_, i) => {
        const o = open[i], h = high[i], l = low[i]
        return o === null || h === null || l === null ? ['-', '-', '-', '-'] : [o, data.close[i], l, h]
      })
      const vols = data.dates.map((_, i) => {
        const v = volume?.[i]
        if (v === null || v === undefined) return null
        return { value: v / 100, itemStyle: { color: data.close[i] >= (open[i] ?? 0) ? UP : DOWN } }
      })
      return {
        animation: false,
        tooltip: { ...tooltipBase, axisPointer: { type: 'cross' } },
        legend: { top: 0, left: 0, textStyle: { color: ink, fontSize: 12 }, itemWidth: 14, itemHeight: 2 },
        axisPointer: { link: [{ xAxisIndex: 'all' }] },
        grid: [{ left: 64, right: 24, top: 34, height: '60%' }, { left: 64, right: 24, top: '75%', bottom: 48 }],
        xAxis: [
          { type: 'category', data: data.dates, axisLine: { lineStyle: { color: line } }, axisLabel: { show: false }, axisTick: { show: false } },
          { type: 'category', gridIndex: 1, data: data.dates, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink, fontSize: 11 } },
        ],
        yAxis: [
          { type: 'value', scale: true, splitLine: { lineStyle: { color: line, type: 'dashed' } }, axisLabel: { color: ink, fontSize: 11 } },
          { type: 'value', gridIndex: 1, splitLine: { show: false }, axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => (v >= 1e4 ? `${(v / 1e4).toFixed(0)}万` : `${v}`) } },
        ],
        dataZoom: [
          { type: 'inside', xAxisIndex: [0, 1], ...zoom },
          { type: 'slider', xAxisIndex: [0, 1], height: 18, bottom: 8, borderColor: cssVar('--line'), textStyle: { color: ink }, ...zoom },
        ],
        series: [
          { name: data.title, type: 'candlestick', data: k, itemStyle: { color: UP, color0: DOWN, borderColor: UP, borderColor0: DOWN }, markPoint },
          ...lines,
          { name: '成交量（手）', type: 'bar', xAxisIndex: 1, yAxisIndex: 1, data: vols },
        ],
      }
    }
    return {
      animation: false,
      grid: { left: 64, right: 24, top: 36, bottom: 54 },
      tooltip: { ...tooltipBase, valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(data.digits ?? 3) : '—') },
      legend: { top: 0, left: 0, textStyle: { color: ink, fontSize: 12 }, itemWidth: 14, itemHeight: 2 },
      xAxis: { type: 'category', data: data.dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink, fontSize: 11 } },
      yAxis: { type: 'value', scale: true, splitLine: { lineStyle: { color: line, type: 'dashed' } }, axisLabel: { color: ink, fontSize: 11 } },
      dataZoom: [
        { type: 'inside', start: 70 },
        { type: 'slider', height: 18, bottom: 8, start: 70, borderColor: cssVar('--line'), textStyle: { color: ink } },
      ],
      series: [
        { name: data.title, type: 'line', data: data.close, showSymbol: false, lineStyle: { width: 1.6, color: cssVar('--accent') }, itemStyle: { color: cssVar('--accent') }, markPoint },
        ...lines,
      ],
    }
  }, [data, theme])
  return <EChart option={option} height={height} />
}
