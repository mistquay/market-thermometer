import { useMemo, useState } from 'react'
import { AlertCircle, ArrowDownRight, ArrowUpRight, Info } from 'lucide-react'
import type { BtResult } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { fmt, pct, TIER_TEXT } from './temperature'
import { useTheme } from './theme'

const SOURCES = [
  { key: 'thermometer', label: '温度计信号', desc: '择时温度状态机触发（不含观察级）', colorVar: '--accent' },
  { key: 'valuation', label: '估值档切换', desc: 'ERP 估值进入便宜 / 昂贵档', colorVar: '--t-hot-1' },
  { key: 'ma200', label: '200 日均线', desc: '指数收盘上穿 / 跌破 MA200', colorVar: '--dim-2' },
] as const

type SourceKey = (typeof SOURCES)[number]['key']
type PeriodKey = 'all' | 'in' | 'out'

function dirClass(x: number | null) {
  if (x === null || Math.abs(x) < 0.0005) return ''
  return x > 0 ? 'up' : 'down'
}

/** 根据买卖方向判断这次前瞻收益是否“顺方向”（买后涨 / 卖后跌）。 */
function isFavorable(val: number | null, side: 1 | -1) {
  if (val === null) return false
  return side > 0 ? val > 0 : val < 0
}

export default function BacktestSignals({ r, split }: { r: BtResult; split: string }) {
  const [side, setSide] = useState<1 | -1>(1)
  const [period, setPeriod] = useState<PeriodKey>('all')
  const [detailSrc, setDetailSrc] = useState<SourceKey>('thermometer')
  const { theme } = useTheme()

  const horizons = r.config.horizons ?? []
  const groups = r.groups ?? []

  const groupOf = (src: SourceKey) => groups.find((g) => g.source === src && g.side === side && g.period === period)

  // 中位前瞻收益对比柱状图（温度计 vs 估值档 vs 200日均线 vs 任意一天基准）
  const barOption = useMemo<EChartsOption | null>(() => {
    if (horizons.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')

    const toPct = (v: number | null | undefined) => (v === null || v === undefined ? null : +(v * 100).toFixed(2))

    const series = [
      ...SOURCES.map((s) => {
        const g = groups.find((x) => x.source === s.key && x.side === side && x.period === period)
        const c = cssVar(s.colorVar)
        return {
          name: s.label,
          type: 'bar' as const,
          barMaxWidth: 28,
          itemStyle: { color: c, borderRadius: [4, 4, 0, 0] },
          data: horizons.map((_, idx) => toPct(g?.horizons[idx]?.median)),
        }
      }),
      {
        name: '任意一天买入（基准）',
        type: 'bar' as const,
        barMaxWidth: 28,
        itemStyle: { color: ink, opacity: 0.45, borderRadius: [4, 4, 0, 0] },
        data: horizons.map((_, idx) => toPct(r.baseRate[idx]?.median)),
      },
    ]

    return {
      animation: false,
      grid: { left: 52, right: 20, top: 38, bottom: 28 },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        valueFormatter: (v: unknown) => (typeof v === 'number' ? `${v > 0 ? '+' : ''}${v.toFixed(2)}%` : '—'),
      },
      legend: { top: 0, left: 0, textStyle: { color: ink, fontSize: 12 }, itemWidth: 12, itemHeight: 8 },
      xAxis: {
        type: 'category',
        data: horizons.map((h) => `信号后 ${h} 交易日`),
        axisLine: { lineStyle: { color: line } },
        axisTick: { show: false },
        axisLabel: { color: ink, fontSize: 12 },
      },
      yAxis: {
        type: 'value',
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v > 0 ? '+' : ''}${v}%` },
      },
      series,
    }
  }, [groups, horizons, period, r.baseRate, side, theme])

  // 明细列表：按当前方向、区间、选中来源筛选
  const detailSignals = useMemo(() => {
    const all = (r.signals ?? []).filter((s) => s.source === detailSrc && s.side === side)
    const byPeriod = all.filter((s) => {
      if (period === 'in') return s.date < split
      if (period === 'out') return s.date >= split
      return true
    })
    return byPeriod.slice().reverse()
  }, [r.signals, detailSrc, side, period, split])

  return (
    <div className="flex flex-col gap-4">
      {/* 顶部清晰筛选条 */}
      <section className="panel flex flex-wrap items-center justify-between gap-3 px-5 py-3.5">
        <div className="flex flex-wrap items-center gap-3">
          <div className="seg" role="group" aria-label="信号方向">
            <button type="button" className={side === 1 ? 'on' : ''} onClick={() => setSide(1)}>
              底部 / 买入信号
            </button>
            <button type="button" className={side === -1 ? 'on' : ''} onClick={() => setSide(-1)}>
              顶部 / 卖出信号
            </button>
          </div>
          <div className="seg" role="group" aria-label="评估区间">
            <button type="button" className={period === 'all' ? 'on' : ''} onClick={() => setPeriod('all')}>
              全部区间
            </button>
            <button type="button" className={period === 'in' ? 'on' : ''} onClick={() => setPeriod('in')}>
              样本内 (&lt; {split.slice(0, 4)})
            </button>
            <button type="button" className={period === 'out' ? 'on' : ''} onClick={() => setPeriod('out')}>
              样本外 (≥ {split.slice(0, 4)})
            </button>
          </div>
        </div>

        <div className="flex items-center gap-1.5 text-xs text-[var(--ink-2)]">
          <Info className="h-3.5 w-3.5 shrink-0 text-[var(--accent)]" />
          {side === 1 ? (
            <span>买入看多：发出后<strong>上涨为胜</strong>，对比“随机买入”看是否超额，MAE 为 60 日内最大浮亏</span>
          ) : (
            <span>卖出避险：发出后<strong>下跌为胜</strong>（成功躲过回调），MAE 为 60 日内最大踏空涨幅</span>
          )}
        </div>
      </section>

      {/* 3 列横向对比卡：温度计 vs 估值档 vs 200日均线 */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {SOURCES.map((src) => {
          const g = groupOf(src.key)
          const selected = detailSrc === src.key
          const color = `var(${src.colorVar})`
          return (
            <div
              key={src.key}
              onClick={() => setDetailSrc(src.key)}
              role="button"
              tabIndex={0}
              onKeyDown={(e) => e.key === 'Enter' && setDetailSrc(src.key)}
              className={`panel relative cursor-pointer overflow-hidden p-4 transition-colors ${
                selected ? 'border-[var(--accent)]' : 'hover:border-[var(--line-strong)]'
              }`}
            >
              <div className="absolute inset-x-0 top-0 h-[2px]" style={{ background: color }} />
              <div className="flex items-start justify-between gap-2">
                <div>
                  <div className="flex items-center gap-2">
                    <span className="h-2.5 w-2.5 rounded-full" style={{ background: color }} />
                    <strong className="text-sm font-semibold text-[var(--ink)]">{src.label}</strong>
                  </div>
                  <div className="mt-0.5 text-[11.5px] text-[var(--ink-3)]">{src.desc}</div>
                </div>
                <div className="flex shrink-0 items-center gap-1.5">
                  <span className="tag tag-muted mono">{g?.n ?? 0} 次</span>
                  {g && g.n > 0 && g.n < 5 && (
                    <span className="tag tag-warn" title="样本量偏少，统计显著性有限">
                      <AlertCircle className="h-3 w-3" />
                      样本少
                    </span>
                  )}
                </div>
              </div>

              {!g || g.n === 0 ? (
                <div className="py-8 text-center text-xs text-[var(--ink-3)]">该区间内无有效信号</div>
              ) : (
                <>
                  <div className="mt-4 space-y-2.5">
                    {horizons.map((h, idx) => {
                      const stat = g.horizons[idx]
                      const base = r.baseRate[idx]
                      const wr = stat?.winRate ?? null
                      const med = stat?.median ?? null
                      const baseMed = base?.median ?? null
                      const edge = med !== null && baseMed !== null ? med - baseMed : null
                      const goodEdge = edge !== null && (side > 0 ? edge > 0 : edge < 0)

                      return (
                        <div key={h} className="rounded-lg bg-[var(--bg-subtle)] px-3 py-2">
                          <div className="flex items-center justify-between text-xs">
                            <span className="font-medium text-[var(--ink-2)]">{h} 交易日后</span>
                            <div className="flex items-center gap-2 mono">
                              <span className={`font-semibold ${dirClass(med)}`}>{pct(med)}</span>
                              {edge !== null && (
                                <span
                                  className={`text-[11px] ${goodEdge ? 'text-[var(--ok)]' : 'text-[var(--ink-3)]'}`}
                                  title={`同期任意一天买入中位收益 ${pct(baseMed)}`}
                                >
                                  ({edge > 0 ? '+' : ''}
                                  {(edge * 100).toFixed(1)}% vs 基准)
                                </span>
                              )}
                            </div>
                          </div>
                          <div className="mt-1.5 flex items-center gap-2.5">
                            <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-[var(--panel-2)]">
                              <div
                                className="h-full rounded-full transition-all"
                                style={{
                                  width: `${wr !== null ? Math.round(wr * 100) : 0}%`,
                                  background:
                                    wr === null
                                      ? 'var(--line)'
                                      : wr >= 0.55
                                        ? 'var(--ok)'
                                        : wr >= 0.45
                                          ? 'var(--accent)'
                                          : 'var(--warn)',
                                }}
                              />
                            </div>
                            <span className="mono w-16 text-right text-[11px] text-[var(--ink-2)]">
                              胜率 {wr === null ? '—' : `${Math.round(wr * 100)}%`}
                            </span>
                          </div>
                        </div>
                      )
                    })}
                  </div>

                  <div className="mt-3 flex items-center justify-between border-t border-[var(--line)] pt-2.5 text-xs">
                    <span className="text-[var(--ink-2)]">
                      {side === 1 ? '60日最大浮亏中位 (MAE)' : '60日最大踏空中位 (MAE)'}
                    </span>
                    <span className={`mono font-semibold ${side === 1 ? 'down' : 'up'}`}>{pct(g.maeMedian)}</span>
                  </div>
                </>
              )}
            </div>
          )
        })}
      </div>

      {/* 中位前瞻收益直观柱状图 */}
      {barOption && (
        <section className="panel overflow-hidden">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] px-5 py-3">
            <strong className="text-sm font-semibold">
              {side === 1 ? '买入信号' : '卖出信号'} · 前瞻中位涨跌幅对比
            </strong>
            <span className="text-xs text-[var(--ink-2)]">
              基准为任意一天随机买入的同期中位收益：
              {horizons.map((h, i) => `${h}日 ${pct(r.baseRate[i]?.median)}`).join(' · ')}
            </span>
          </div>
          <div className="p-3">
            <EChart option={barOption} height={240} />
          </div>
        </section>
      )}

      {/* 可切换来源的信号明细表 */}
      <section className="panel overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3">
          <div className="flex flex-wrap items-center gap-3">
            <strong className="text-sm font-semibold">历史信号明细</strong>
            <div className="seg">
              {SOURCES.map((s) => (
                <button key={s.key} type="button" className={detailSrc === s.key ? 'on' : ''} onClick={() => setDetailSrc(s.key)}>
                  {s.label}
                </button>
              ))}
            </div>
            <span className="tag tag-muted mono">{detailSignals.length} 条</span>
          </div>
          <span className="text-xs text-[var(--ink-3)]">
            以 60 日后涨跌标记命中方向；点击上方卡片也可快速切换来源
          </span>
        </div>

        {detailSignals.length === 0 ? (
          <div className="empty">当前筛选条件下没有信号记录。</div>
        ) : (
          <div className="table-scroll scroll-y">
            <table className="table static">
              <thead>
                <tr>
                  <th>日期</th>
                  <th>区间</th>
                  <th>类型</th>
                  <th className="num">收盘</th>
                  <th className="num">T_s</th>
                  <th>估值档</th>
                  {horizons.map((h) => (
                    <th key={h} className="num">
                      {h} 日后
                    </th>
                  ))}
                  <th className="num">60日 MAE</th>
                  <th>触发说明</th>
                </tr>
              </thead>
              <tbody>
                {detailSignals.map((s, i) => {
                  const isOos = s.date >= split
                  const refIdx = horizons.indexOf(60) >= 0 ? horizons.indexOf(60) : 0
                  const hit = isFavorable(s.rets[refIdx] ?? null, side)
                  return (
                    <tr key={`${s.date}-${i}`}>
                      <td className="mono nowrap">{s.date}</td>
                      <td>
                        <span className={`tag ${isOos ? 'tag-cold' : 'tag-muted'}`}>{isOos ? '样本外' : '样本内'}</span>
                      </td>
                      <td className="nowrap">
                        <span className={`tag ${side > 0 ? 'tag-cold' : 'tag-hot'}`}>
                          {side > 0 ? <ArrowUpRight className="h-3 w-3" /> : <ArrowDownRight className="h-3 w-3" />}
                          {side > 0 ? '买入' : '卖出'}
                        </span>
                        {s.observe && (
                          <span className="tag tag-muted ml-1">观察</span>
                        )}
                      </td>
                      <td className="num mono">{fmt(s.close, 2)}</td>
                      <td className="num mono">{fmt(s.tS)}</td>
                      <td className="nowrap text-xs">{s.tier ? TIER_TEXT[s.tier] : '—'}</td>
                      {s.rets.map((x, k) => (
                        <td key={k} className={`num mono ${dirClass(x)} ${k === refIdx && x !== null ? (hit ? 'font-semibold' : 'opacity-75') : ''}`}>
                          {pct(x)}
                        </td>
                      ))}
                      <td className="num mono text-[var(--ink-2)]">{pct(s.mae)}</td>
                      <td className="max-w-xs truncate text-xs text-[var(--ink-2)]" title={s.reason}>
                        {s.reason || '—'}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
