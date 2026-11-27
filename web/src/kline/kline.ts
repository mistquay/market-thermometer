// K 线渲染器插件：行情页只组装与图表库无关的 KlineData，具体画法由可替换的渲染器实现。
// 新增渲染器 = 实现 KlineProps 组件 + 在 RENDERERS 里登记一行；按需懒加载，不进主包。
import { lazy, useState, type ComponentType, type LazyExoticComponent } from 'react'

export type Num = number | null

export type KlineMarker = {
  date: string
  side: 'buy' | 'sell'
  text: string
  price?: number
  detail?: string
}

/** 已按当前复权方式换算好的价格数据，与日期一一对应。 */
export type KlineData = {
  dates: string[]
  close: number[]
  open: Num[] | null // 没有开高低（如场外基金、无扩展数据）时为 null，渲染器画收盘价折线
  high: Num[] | null
  low: Num[] | null
  volume: Num[] | null // 股
  lines: { name: string; color: string; data: Num[] }[] // 均线等叠加线
  markers?: KlineMarker[] // 模拟盘成交买卖点等事件标记
  title: string // 主序列名，如 “K 线（前复权）”
  /** 日内 K 线：与 dates 一一对应的 Unix 秒（UTC）；有它时按时间轴画（显示本地时间），dates 只用作图例文字。 */
  times?: number[]
  digits?: number // 价格小数位，默认 3
  volUnit?: string // 成交量单位，默认 “股”（按万 / 亿股显示）；给出时按原值加单位显示
}

export type KlineProps = { data: KlineData; height: number }

export type KlineRenderer = {
  id: string
  label: string
  Component: LazyExoticComponent<ComponentType<KlineProps>>
}

export const RENDERERS: KlineRenderer[] = [
  { id: 'lwc', label: 'TradingView', Component: lazy(() => import('./LwcKline')) },
  { id: 'echarts', label: 'ECharts', Component: lazy(() => import('./EChartsKline')) },
]

const KEY = 'mt.klineRenderer'

/** 当前选用的渲染器（记在浏览器本地，默认第一个）。 */
export function useKlineRenderer(): [KlineRenderer, (id: string) => void] {
  const [id, setId] = useState(() => localStorage.getItem(KEY) ?? RENDERERS[0].id)
  const r = RENDERERS.find((x) => x.id === id) ?? RENDERERS[0]
  return [r, (next: string) => { localStorage.setItem(KEY, next); setId(next) }]
}

/** A 股习惯：红涨绿跌。 */
export const UP = '#d8453b'
export const DOWN = '#1f8a4c'
