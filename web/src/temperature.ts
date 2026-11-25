// 温度、估值档的展示规则（区间划分与设计文档 §4.1 一致）。

import type { Num } from './api'

export type Zone = { key: string; label: string; color: string }

const Z = {
  extremeCold: { key: 'extreme-cold', label: '深度冰点', color: 'var(--t-cold-2)' },
  cold: { key: 'cold', label: '偏冷', color: 'var(--t-cold-1)' },
  neutral: { key: 'neutral', label: '中性', color: 'var(--t-neutral)' },
  hot: { key: 'hot', label: '升温', color: 'var(--t-hot-1)' },
  extremeHot: { key: 'extreme-hot', label: '过热', color: 'var(--t-hot-2)' },
} satisfies Record<string, Zone>

/** ≤20 深度冰点｜(20,35] 偏冷｜(35,70) 中性｜[70,80) 升温｜≥80 过热 */
export function zoneOf(x: Num): Zone | null {
  if (x === null) return null
  if (x <= 20) return Z.extremeCold
  if (x <= 35) return Z.cold
  if (x < 70) return Z.neutral
  if (x < 80) return Z.hot
  return Z.extremeHot
}

export const TIER_TEXT: Record<string, string> = {
  cheap: '便宜',
  fair: '合理',
  expensive: '昂贵',
}

export const TIER_COLOR: Record<string, string> = {
  cheap: 'var(--t-cold-2)',
  fair: 'var(--t-neutral)',
  expensive: 'var(--t-hot-2)',
}

export function fmt(x: Num, digits = 1, suffix = '') {
  return x === null ? '—' : `${x.toFixed(digits)}${suffix}`
}

export const EVENT_TEXT: Record<string, string> = {
  ARMED_BOTTOM: '底部武装',
  ARMED_TOP: '顶部武装',
  BUY_DIP: '底部信号',
  SELL_PEAK: '顶部信号',
  DISARMED: '武装失效',
  VALUATION_TIER_CHANGE: '估值档切换',
  DATA_DEGRADED: '数据降级',
}

export const PHASE_TEXT: Record<string, string> = {
  IDLE: '观望',
  BOTTOM_ARMED: '底部武装中',
  TOP_ARMED: '顶部武装中',
  BOTTOM_COOLDOWN: '底部信号后冷却',
  TOP_COOLDOWN: '顶部信号后冷却',
}

export function pct(x: number | null | undefined, digits = 1) {
  if (x === null || x === undefined) return '—'
  return `${x > 0 ? '+' : ''}${(x * 100).toFixed(digits)}%`
}
