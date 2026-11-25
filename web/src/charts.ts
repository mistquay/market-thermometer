import { cssVar, type EChartsOption } from './EChart'
import type { SignalEvent } from './api'

type Marker = { date: string; close: number | null; type: string; observe?: boolean }

/** 价格折线 + 底部/顶部信号标记（▲ 买、▼ 卖，观察级为空心）。 */
export function priceWithSignals(dates: string[], closes: (number | null)[], events: Marker[], name = '沪深300'): EChartsOption {
  const ink = cssVar('--ink-2'), line = cssVar('--line')
  const buy = cssVar('--t-cold-2'), sell = cssVar('--t-hot-2')
  const marks = events
    .filter((e) => (e.type === 'BUY_DIP' || e.type === 'SELL_PEAK') && e.close !== null)
    .map((e) => {
      const isBuy = e.type === 'BUY_DIP'
      const color = isBuy ? buy : sell
      return {
        coord: [e.date, e.close],
        symbol: isBuy ? 'triangle' : 'path://M0,0 L10,0 L5,10 Z',
        symbolSize: 11,
        symbolOffset: [0, isBuy ? 12 : -12],
        itemStyle: e.observe ? { color: 'transparent', borderColor: color, borderWidth: 1.5 } : { color },
        label: { show: false },
        name: isBuy ? (e.observe ? '底部信号（观察）' : '底部信号') : '顶部信号',
      }
    })
  return {
    animation: false,
    grid: { left: 64, right: 24, top: 16, bottom: 64 },
    tooltip: { trigger: 'axis' },
    xAxis: { type: 'category', data: dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
    yAxis: { type: 'value', scale: true, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
    dataZoom: [{ type: 'inside' }, { type: 'slider', height: 20, bottom: 12, borderColor: line, textStyle: { color: ink } }],
    series: [
      {
        name, type: 'line', data: closes, showSymbol: false,
        lineStyle: { width: 1.2, color: ink }, itemStyle: { color: ink },
        markPoint: { data: marks, tooltip: { formatter: (p: { name: string; data: { coord: [string, number] } }) => `${p.data.coord[0]} ${p.name}` } },
      },
    ],
  }
}

export function asMarkers(evs: SignalEvent[] | null | undefined): Marker[] {
  return (evs ?? []).map((e) => ({ date: e.date, close: e.close, type: e.type, observe: e.observe }))
}
