import { useCallback, useEffect, useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, FlaskConical, Layers, PanelLeftClose, PanelLeftOpen, Play, RotateCcw, Save, ShieldCheck, SlidersHorizontal, TrendingUp } from 'lucide-react'
import { api, type AblationResp, type BacktestResp, type FullParams, type ParamsResp } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { pct, TIER_TEXT } from './temperature'
import { useTheme } from './theme'
import BacktestSignals from './BacktestSignals'
import Robustness from './Robustness'
import { useConfirm, useToast } from './ui/Feedback'

type Field = { path: string; label: string; step?: number; hint?: string; bool?: boolean }
type Group = { title: string; defaultOpen?: boolean; fields: Field[] }
type TabKey = 'equity' | 'signals' | 'robust' | 'ablation'

const SCHEMA: Group[] = [
  {
    title: '规则开关',
    defaultOpen: true,
    fields: [
      { path: 'skipTPct', label: '去掉二次分位（方案 C）', bool: true, hint: '温度直接用加权得分平滑，不再取近 3 年分位' },
      { path: 'signal.topOnlyExpensive', label: '顶部信号只在估值昂贵时', bool: true, hint: '估值便宜/合理时不发卖出信号' },
    ],
  },
  {
    title: '分层持仓',
    defaultOpen: true,
    fields: [
      { path: 'position.baseCheap', label: '底仓（便宜）', step: 0.05 },
      { path: 'position.baseFair', label: '底仓（合理）', step: 0.05 },
      { path: 'position.baseExpensive', label: '底仓（昂贵）', step: 0.05 },
      { path: 'position.step', label: '信号加减仓', step: 0.05, hint: '底部信号后加、顶部信号后减，直到反向信号' },
    ],
  },
  {
    title: '估值层',
    fields: [
      { path: 'cheapBelow', label: '便宜线 V ≤' },
      { path: 'expensiveAbove', label: '昂贵线 V ≥' },
      { path: 'tierHysteresis', label: '估值档滞回', hint: '离开某档需越过阈值的幅度' },
      { path: 'valuationWindow', label: 'ERP 分位窗口（日）', step: 10 },
    ],
  },
  {
    title: '择时层',
    fields: [
      { path: 'weights.0', label: '权重：资金热度', step: 0.1 },
      { path: 'weights.1', label: '权重：杠杆情绪', step: 0.1 },
      { path: 'weights.2', label: '权重：市场广度', step: 0.1 },
      { path: 'weights.3', label: '权重：价格动量', step: 0.1 },
      { path: 'timingWindow', label: '分位窗口（日）', step: 10 },
      { path: 'emaSpan', label: 'T_s 平滑（日）' },
      { path: 'extreme', label: '子维度极端区', hint: '≤此值算冰点，≥100−此值算过热' },
    ],
  },
  {
    title: '底部信号',
    fields: [
      { path: 'signal.armLowCheap', label: `武装线（${TIER_TEXT.cheap}）` },
      { path: 'signal.armLowFair', label: `武装线（${TIER_TEXT.fair}）` },
      { path: 'signal.armLowExpensive', label: `武装线（${TIER_TEXT.expensive}）` },
      { path: 'signal.rebound', label: '回暖幅度' },
      { path: 'signal.riseDays', label: '连续上升天数' },
      { path: 'signal.cooldownLowEx', label: '冷却解除 T_s >' },
    ],
  },
  {
    title: '顶部信号',
    fields: [
      { path: 'signal.armHighCheap', label: `武装线（${TIER_TEXT.cheap}）` },
      { path: 'signal.armHighFair', label: `武装线（${TIER_TEXT.fair}）` },
      { path: 'signal.armHighExpensive', label: `武装线（${TIER_TEXT.expensive}）` },
      { path: 'signal.pullback', label: '回落幅度' },
      { path: 'signal.rsiExit', label: '日 RSI 低于' },
      { path: 'signal.cooldownHighEx', label: '冷却解除 T_s <' },
    ],
  },
  {
    title: '状态机通用',
    fields: [
      { path: 'signal.minExtremeDims', label: '武装需极端维度数' },
      { path: 'signal.divergenceGap', label: '背离幅度' },
      { path: 'signal.armTimeout', label: '武装超时（日）' },
      { path: 'signal.disarmMid', label: '武装失效中线' },
    ],
  },
]

const STRAT_META: Record<string, { short: string; colorVar: string; rule: string }> = {
  thermometer: { short: '温度计', colorVar: '--accent', rule: '温度计底部信号满仓，顶部信号空仓（观察级不入仓）' },
  layered: { short: '分层持仓', colorVar: '--t-cold-2', rule: '按估值档设定底仓，再用温度计底部/顶部信号加减仓' },
  valuation: { short: '估值档', colorVar: '--t-hot-1', rule: '估值进入便宜档满仓，进入昂贵档空仓' },
  ma200: { short: '200日均线', colorVar: '--dim-2', rule: '收盘价站上 200 日均线满仓，跌破空仓' },
  hold: { short: '买入持有', colorVar: '--ink-2', rule: '全程 100% 满仓沪深300（基准对照）' },
}

function getPath(p: FullParams, path: string): unknown {
  return path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown>)?.[k], p)
}

function setPath(p: FullParams, path: string, v: number | boolean): FullParams {
  const next = structuredClone(p)
  const keys = path.split('.')
  let o = next as unknown as Record<string, unknown>
  for (const k of keys.slice(0, -1)) o = o[k] as Record<string, unknown>
  o[keys[keys.length - 1]] = v
  return next
}

function dirClass(x: number | null) {
  if (x === null || Math.abs(x) < 0.0005) return ''
  return x > 0 ? 'up' : 'down'
}

export default function Backtest() {
  const [meta, setMeta] = useState<ParamsResp | null>(null)
  const [params, setParams] = useState<FullParams | null>(null)
  const [from, setFrom] = useState('2014-01-01')
  const [split, setSplit] = useState('2021-01-01')
  const [costBps, setCostBps] = useState(0)
  const [res, setRes] = useState<BacktestResp | null>(null)
  const [ablation, setAblation] = useState<AblationResp | null>(null)
  const [ablationBusy, setAblationBusy] = useState(false)
  const [tab, setTab] = useState<TabKey>('equity')
  const [selectedStrat, setSelectedStrat] = useState<string>('layered')
  const [openGroups, setOpenGroups] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(SCHEMA.map((g) => [g.title, !!g.defaultOpen])),
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [name, setName] = useState('')
  const [paramsOpen, setParamsOpen] = useState(true)
  const { theme } = useTheme()
  const confirm = useConfirm()
  const toast = useToast()

  const run = useCallback(
    async (p: FullParams | null) => {
      setBusy(true)
      setError('')
      try {
        setRes(await api.backtest(p, { from, split, costBps }))
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      } finally {
        setBusy(false)
      }
    },
    [from, split, costBps],
  )

  const runAblation = useCallback(
    async (p: FullParams | null) => {
      setAblationBusy(true)
      setError('')
      try {
        setAblation(await api.ablation(p, { from, split, costBps }))
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e))
      } finally {
        setAblationBusy(false)
      }
    },
    [from, split, costBps],
  )

  const loadMeta = useCallback(() => {
    return api.params().then((m) => {
      setMeta(m)
      const active = m.sets.find((s) => s.active)
      setParams((cur) => cur ?? structuredClone(active?.params ?? m.defaults))
      return active?.params ?? m.defaults
    })
  }, [])

  useEffect(() => {
    loadMeta().then(
      (p) => run(p),
      (e) => setError(e.message),
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const active = meta?.sets.find((s) => s.active)
  const dirty = res && active && res.paramsVersion !== active.version

  async function save(activate: boolean) {
    if (!params) return
    setError('')
    setNotice('')
    try {
      await api.saveParams(name.trim(), params, activate)
      await loadMeta()
      setNotice(activate ? '已保存并设为生效，总览与信号已按新参数重算。' : '已保存参数集。')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const r = res?.result
  const activeStratObj = r?.strategies.find((s) => s.name === selectedStrat) ?? r?.strategies[0]
  const activeMeta = STRAT_META[activeStratObj?.name ?? 'layered'] ?? STRAT_META.layered

  // 净值 + 联动回撤双联图：高亮当前选中策略，其余弱化，并标出样本外分界线
  const equityOption = useMemo<EChartsOption | null>(() => {
    if (!r || r.dates.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const splitDate = r.dates.find((d) => d >= split)
    const ddOf = (eq: number[]) => {
      let peak = 0
      return eq.map((v) => {
        if (v > peak) peak = v
        return peak > 0 ? +((v / peak - 1) * 100).toFixed(2) : 0
      })
    }

    return {
      animation: false,
      axisPointer: { link: [{ xAxisIndex: 'all' }] },
      grid: [
        { left: 54, right: 24, top: 38, height: '54%' },
        { left: 54, right: 24, top: '69%', bottom: 48 },
      ],
      tooltip: {
        trigger: 'axis',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(2) : String(v)),
      },
      legend: { top: 0, left: 0, textStyle: { color: ink, fontSize: 12 }, itemWidth: 14, itemHeight: 3 },
      xAxis: [
        {
          type: 'category',
          data: r.dates,
          boundaryGap: false,
          axisLine: { lineStyle: { color: line } },
          axisTick: { show: false },
          axisLabel: { show: false },
        },
        {
          type: 'category',
          gridIndex: 1,
          data: r.dates,
          boundaryGap: false,
          axisLine: { lineStyle: { color: line } },
          axisTick: { show: false },
          axisLabel: { color: ink, fontSize: 11 },
        },
      ],
      yAxis: [
        {
          type: 'value',
          scale: true,
          name: '净值',
          nameTextStyle: { color: ink, fontSize: 11 },
          splitLine: { lineStyle: { color: line, type: 'dashed' } },
          axisLabel: { color: ink, fontSize: 11 },
        },
        {
          type: 'value',
          gridIndex: 1,
          max: 0,
          name: '回撤%',
          nameTextStyle: { color: ink, fontSize: 11 },
          splitLine: { lineStyle: { color: line, type: 'dashed' } },
          axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v}%` },
        },
      ],
      dataZoom: [
        { type: 'inside', xAxisIndex: [0, 1] },
        { type: 'slider', xAxisIndex: [0, 1], height: 18, bottom: 8, borderColor: cssVar('--line'), textStyle: { color: ink } },
      ],
      series: [
        ...r.strategies.map((s, idx) => {
          const c = cssVar(STRAT_META[s.name]?.colorVar ?? '--ink-2')
          const isSel = s.name === (activeStratObj?.name ?? '')
          return {
            name: s.label,
            type: 'line' as const,
            xAxisIndex: 0,
            yAxisIndex: 0,
            data: s.equity,
            showSymbol: false,
            z: isSel ? 10 : 2,
            lineStyle: { width: isSel ? 2.5 : 1.2, color: c, opacity: isSel ? 1 : 0.42 },
            itemStyle: { color: c },
            markLine:
              idx === 0 && splitDate
                ? {
                    symbol: 'none',
                    silent: true,
                    lineStyle: { color: cssVar('--warn'), type: 'dashed', width: 1.2, opacity: 0.8 },
                    label: {
                      formatter: `样本外 ${splitDate} →`,
                      color: cssVar('--warn'),
                      backgroundColor: cssVar('--panel'),
                      padding: [2, 6],
                      borderRadius: 4,
                      fontSize: 11,
                      position: 'end' as const,
                    },
                    data: [{ xAxis: splitDate }],
                  }
                : undefined,
          }
        }),
        ...r.strategies.map((s) => {
          const c = cssVar(STRAT_META[s.name]?.colorVar ?? '--ink-2')
          const isSel = s.name === (activeStratObj?.name ?? '')
          return {
            name: `${s.label}回撤`,
            type: 'line' as const,
            xAxisIndex: 1,
            yAxisIndex: 1,
            data: ddOf(s.equity),
            showSymbol: false,
            z: isSel ? 10 : 1,
            lineStyle: { width: isSel ? 1.8 : 0.9, color: c, opacity: isSel ? 0.95 : 0.25 },
            itemStyle: { color: c },
            areaStyle: isSel ? { color: c, opacity: 0.16 } : undefined,
          }
        }),
      ],
    }
  }, [r, activeStratObj?.name, split, theme])

  // 维度消融对比柱状图（全区间分层 vs 样本外分层 + 估值档样本外参考线）
  const ablationOption = useMemo<EChartsOption | null>(() => {
    if (!ablation?.rows?.length) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const accent = cssVar('--accent')
    const cold = cssVar('--t-cold-2')
    const warn = cssVar('--warn')
    const baseOutVal = +(ablation.rows[0].out.valRet * 100).toFixed(1)
    const names = ablation.rows.map((row) => row.label.replace(/（.*?）/g, ''))
    return {
      animation: false,
      grid: { left: 52, right: 24, top: 34, bottom: 54 },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        valueFormatter: (v: unknown) => (typeof v === 'number' ? `${v >= 0 ? '+' : ''}${v.toFixed(1)}%` : String(v)),
      },
      legend: { top: 0, left: 0, textStyle: { color: ink, fontSize: 12 }, itemWidth: 12, itemHeight: 8 },
      xAxis: {
        type: 'category',
        data: names,
        axisLine: { lineStyle: { color: line } },
        axisLabel: { color: ink, fontSize: 11, rotate: 18, interval: 0 },
      },
      yAxis: {
        type: 'value',
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v}%` },
      },
      series: [
        {
          name: '全区间·分层收益',
          type: 'bar',
          barMaxWidth: 22,
          data: ablation.rows.map((row) => +(row.all.layeredRet * 100).toFixed(1)),
          itemStyle: { color: accent, borderRadius: [4, 4, 0, 0] },
        },
        {
          name: '样本外·分层收益',
          type: 'bar',
          barMaxWidth: 22,
          data: ablation.rows.map((row) => +(row.out.layeredRet * 100).toFixed(1)),
          itemStyle: { color: cold, borderRadius: [4, 4, 0, 0] },
          markLine: {
            silent: true,
            symbol: 'none',
            lineStyle: { color: warn, type: 'dashed', width: 1.4 },
            label: { formatter: `估值档样本外基线 ${baseOutVal >= 0 ? '+' : ''}${baseOutVal}%`, color: warn, fontSize: 11, position: 'insideEndTop' },
            data: [{ yAxis: baseOutVal }],
          },
        },
      ],
    }
  }, [ablation, theme])

  // 选中策略对应的买卖信号点 + 每日仓位双联图
  const inspector = useMemo(() => {
    if (!r || !activeStratObj || r.dates.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const buyColor = cssVar('--t-cold-2')
    const sellColor = cssVar('--t-hot-2')
    const stratColor = cssVar(activeMeta.colorVar)
    const splitDate = r.dates.find((d) => d >= split)

    const allSigs = r.signals ?? []
    const stratName = activeStratObj.name
    const matched = allSigs.filter((s) => {
      if (stratName === 'thermometer') return s.source === 'thermometer'
      if (stratName === 'layered') return s.source === 'thermometer' || s.source === 'valuation'
      if (stratName === 'valuation') return s.source === 'valuation'
      if (stratName === 'ma200') return s.source === 'ma200'
      return false
    })

    const marks = matched
      .filter((s) => s.close !== null)
      .map((s) => {
        const isBuy = s.side > 0
        const isValInLayered = stratName === 'layered' && s.source === 'valuation'
        const color = isValInLayered ? cssVar('--t-hot-1') : isBuy ? buyColor : sellColor
        const label =
          s.source === 'valuation'
            ? isBuy
              ? '估值进入便宜'
              : '估值进入昂贵'
            : s.source === 'ma200'
              ? isBuy
                ? '上穿 MA200 买入'
                : '跌破 MA200 卖出'
              : isBuy
                ? s.observe
                  ? '温度计底部（观察·不入仓）'
                  : '温度计底部买入'
                : '温度计顶部卖出'
        return {
          coord: [s.date, s.close],
          symbol: isValInLayered ? 'diamond' : isBuy ? 'triangle' : 'path://M0,0 L10,0 L5,10 Z',
          symbolSize: isValInLayered ? 9 : 11,
          symbolOffset: [0, isValInLayered ? 0 : isBuy ? 12 : -12],
          itemStyle: s.observe ? { color: 'transparent', borderColor: color, borderWidth: 1.5 } : { color },
          label: { show: false },
          name: label,
        }
      })

    const hasPos = !!activeStratObj.position && activeStratObj.position.length === r.dates.length
    const posPct = hasPos ? activeStratObj.position!.map((p) => +(p * 100).toFixed(1)) : []

    const option: EChartsOption = {
      animation: false,
      axisPointer: { link: [{ xAxisIndex: 'all' }] },
      tooltip: {
        trigger: 'axis',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
      },
      grid: hasPos
        ? [
            { left: 56, right: 24, top: 24, height: '56%' },
            { left: 56, right: 24, top: '70%', bottom: 48 },
          ]
        : [{ left: 56, right: 24, top: 24, bottom: 52 }],
      xAxis: hasPos
        ? [
            { type: 'category', data: r.dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { show: false }, axisTick: { show: false } },
            { type: 'category', gridIndex: 1, data: r.dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisTick: { show: false }, axisLabel: { color: ink, fontSize: 11 } },
          ]
        : [{ type: 'category', data: r.dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisTick: { show: false }, axisLabel: { color: ink, fontSize: 11 } }],
      yAxis: hasPos
        ? [
            { type: 'value', scale: true, name: '沪深300', nameTextStyle: { color: ink, fontSize: 11 }, splitLine: { lineStyle: { color: line, type: 'dashed' } }, axisLabel: { color: ink, fontSize: 11 } },
            { type: 'value', gridIndex: 1, min: 0, max: 100, interval: 50, name: '仓位%', nameTextStyle: { color: ink, fontSize: 11 }, splitLine: { lineStyle: { color: line, type: 'dashed' } }, axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v}%` } },
          ]
        : [{ type: 'value', scale: true, name: '沪深300', nameTextStyle: { color: ink, fontSize: 11 }, splitLine: { lineStyle: { color: line, type: 'dashed' } }, axisLabel: { color: ink, fontSize: 11 } }],
      dataZoom: [
        { type: 'inside', xAxisIndex: hasPos ? [0, 1] : [0] },
        { type: 'slider', xAxisIndex: hasPos ? [0, 1] : [0], height: 18, bottom: 8, borderColor: cssVar('--line'), textStyle: { color: ink } },
      ],
      series: [
        {
          name: '沪深300',
          type: 'line',
          data: r.close,
          showSymbol: false,
          lineStyle: { width: 1.5, color: inkMain, opacity: 0.85 },
          itemStyle: { color: inkMain },
          markPoint: marks.length
            ? {
                data: marks,
                tooltip: {
                  formatter: (p: { name: string; data: { coord: [string, number] } }) => `${p.data.coord[0]} · ${p.name}`,
                },
              }
            : undefined,
          markLine: splitDate
            ? {
                symbol: 'none',
                silent: true,
                lineStyle: { color: cssVar('--warn'), type: 'dashed', width: 1, opacity: 0.65 },
                label: { show: false },
                data: [{ xAxis: splitDate }],
              }
            : undefined,
        },
        ...(hasPos
          ? [
              {
                name: `${activeMeta.short}仓位`,
                type: 'line' as const,
                step: 'end' as const,
                xAxisIndex: 1,
                yAxisIndex: 1,
                showSymbol: false,
                data: posPct,
                lineStyle: { width: 1.8, color: stratColor },
                itemStyle: { color: stratColor },
                areaStyle: { color: stratColor, opacity: 0.16 },
              },
            ]
          : []),
      ],
    }

    return { option, count: matched.length, hasPos }
  }, [r, activeStratObj, activeMeta, split, theme])

  const thermoSignals = (r?.signals ?? []).filter((s) => s.source === 'thermometer')
  const stratOf = (k: string) => r?.strategies.find((s) => s.name === k)
  const layered = stratOf('layered')
  const thermo = stratOf('thermometer')
  const valBase = stratOf('valuation')
  const holdBase = stratOf('hold')
  const focusStrat = layered ?? thermo

  return (
    <div className="page">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="m-0">策略回测</h1>
            {res && (
              <span className={`tag ${dirty ? 'tag-warn' : 'tag-ok'}`}>
                {dirty ? `试验参数 ${res.paramsVersion}` : '与线上生效参数一致'}
              </span>
            )}
          </div>
          <p className="sub mt-1">
            本地数据即时重算，不改动线上结果；点击下方任意策略行即可联动查看其买卖点、回撤与每日仓位。
            {res && <span className="mono"> · 本次耗时 {res.elapsedMs} ms</span>}
          </p>
        </div>
        <button type="button" className="btn" onClick={() => setParamsOpen((v) => !v)}>
          {paramsOpen ? <PanelLeftClose className="h-4 w-4" /> : <PanelLeftOpen className="h-4 w-4" />}
          {paramsOpen ? '收起参数栏' : '展开参数栏'}
        </button>
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      {notice && (
        <div className="ok-note" role="status">
          {notice}
        </div>
      )}

      <div className={`grid grid-cols-1 gap-4 xl:items-start ${paramsOpen ? 'xl:grid-cols-[310px_1fr]' : 'xl:grid-cols-1'}`}>
        {/* 左侧手风琴参数面板 + 吸顶操作条 */}
        {paramsOpen && (
        <aside className="panel flex flex-col overflow-hidden xl:sticky xl:top-[72px] xl:max-h-[calc(100vh-92px)]">
          <div className="flex items-center justify-between gap-2 border-b border-[var(--line)] bg-[var(--bg-subtle)] px-4 py-3">
            <span className="flex items-center gap-1.5 text-xs font-semibold text-[var(--ink)]">
              <SlidersHorizontal className="h-3.5 w-3.5 text-[var(--accent)]" />
              参数配置
            </span>
            <div className="flex items-center gap-1">
              <button
                type="button"
                className="btn btn-ghost h-7 px-2 text-xs"
                disabled={busy || !active}
                title="恢复当前线上生效参数"
                onClick={() => {
                  const p = structuredClone(active!.params)
                  setParams(p)
                  run(p)
                }}
              >
                <RotateCcw className="h-3 w-3" />
                恢复
              </button>
              <button
                type="button"
                className="btn btn-primary h-7 px-2.5 text-xs"
                disabled={busy || !params}
                onClick={() => run(params)}
              >
                <Play className="h-3 w-3" />
                {busy ? '计算中…' : '运行'}
              </button>
            </div>
          </div>

          <div className="flex-1 overflow-y-auto px-4 py-3">
            <div className="space-y-2 pb-3">
              <label className="field-inline">
                <span>回测起点</span>
                <input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
              </label>
              <label className="field-inline">
                <span>样本外分界</span>
                <input type="date" value={split} onChange={(e) => setSplit(e.target.value)} />
              </label>
              <label className="field-inline" title="单边换手交易成本（基点，10 bps = 万分之十 = 0.1%）">
                <span>交易成本 (bps)</span>
                <input type="number" min={0} max={200} step={5} value={costBps} onChange={(e) => setCostBps(Math.max(0, e.target.valueAsNumber || 0))} />
              </label>
            </div>

            {params && (
              <div className="divide-y divide-[var(--line)] border-t border-[var(--line)]">
                {SCHEMA.map((g) => {
                  const isOpen = !!openGroups[g.title]
                  return (
                    <div key={g.title} className="py-2">
                      <button
                        type="button"
                        onClick={() => setOpenGroups((o) => ({ ...o, [g.title]: !o[g.title] }))}
                        className="flex w-full items-center justify-between py-1 text-left text-xs font-semibold text-[var(--ink)] hover:text-[var(--accent)]"
                      >
                        <span>{g.title}</span>
                        {isOpen ? (
                          <ChevronDown className="h-3.5 w-3.5 text-[var(--ink-3)]" />
                        ) : (
                          <ChevronRight className="h-3.5 w-3.5 text-[var(--ink-3)]" />
                        )}
                      </button>
                      {isOpen && (
                        <div className="mt-2 space-y-2 pb-1">
                          {g.fields.map((f) =>
                            f.bool ? (
                              <label key={f.path} className="field-check text-xs" title={f.hint}>
                                <input
                                  type="checkbox"
                                  className="accent-[var(--accent)]"
                                  checked={!!getPath(params, f.path)}
                                  onChange={(e) => setParams(setPath(params, f.path, e.target.checked))}
                                />
                                <span>{f.label}</span>
                              </label>
                            ) : (
                              <label key={f.path} className="field-inline text-xs" title={f.hint}>
                                <span>{f.label}</span>
                                <input
                                  type="number"
                                  step={f.step ?? 1}
                                  value={(getPath(params, f.path) as number) ?? ''}
                                  onChange={(e) => {
                                    const v = e.target.valueAsNumber
                                    if (!Number.isNaN(v)) setParams(setPath(params, f.path, v))
                                  }}
                                />
                              </label>
                            ),
                          )}
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            )}

            <div className="mt-3 space-y-2.5 border-t border-[var(--line)] pt-3">
              <div className="flex items-center gap-1.5">
                <input
                  className="h-8 min-w-0 flex-1 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-2.5 text-xs text-[var(--ink)] placeholder:text-[var(--ink-3)] focus:border-[var(--accent)] focus:outline-none"
                  placeholder="命名以保存参数集…"
                  value={name}
                  maxLength={40}
                  onChange={(e) => setName(e.target.value)}
                />
                <button className="btn h-8 px-2.5 text-xs" disabled={!name.trim()} onClick={() => save(false)} title="仅保存参数集（不覆盖生效版本）">
                  <Save className="h-3.5 w-3.5" />
                  保存
                </button>
                <button
                  className="btn btn-primary h-8 px-2.5 text-xs"
                  disabled={!name.trim()}
                  onClick={async () => {
                    const ok = await confirm({
                      title: '设为线上生效参数',
                      message: '设为生效后，总览、信号与后续推送都将按这组参数重算并生效。确定继续？',
                      confirmText: '确认生效',
                    })
                    if (ok) {
                      await save(true)
                      toast.success(`参数集「${name.trim()}」已设为生效`)
                    }
                  }}
                >
                  生效
                </button>
              </div>
              <div className="flex items-center justify-between">
                <span className="text-[11px] text-[var(--ink-3)]">快速载入</span>
                <button
                  type="button"
                  className="link text-xs"
                  disabled={busy || !meta}
                  onClick={() => setParams(structuredClone(meta!.defaults))}
                >
                  填入默认值
                </button>
              </div>
              {meta && meta.sets.length > 0 && (
                <div className="flex flex-col gap-1">
                  {meta.sets.map((s) => (
                    <button
                      key={s.version}
                      type="button"
                      onClick={() => setParams(structuredClone(s.params))}
                      className="flex items-center justify-between rounded-md px-2 py-1 text-left text-xs text-[var(--ink-2)] hover:bg-[var(--panel-2)] hover:text-[var(--ink)]"
                    >
                      <span className="truncate">{s.name || s.version}</span>
                      <span className="mono shrink-0 text-[11px] text-[var(--ink-3)]">
                        {s.active ? <span className="text-[var(--ok)]">生效中</span> : s.version.slice(0, 6)}
                      </span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
        </aside>
        )}

        {/* 右侧：核心结论 KPI 条 + 工作区 Tab */}
        <div className="flex min-w-0 flex-col gap-4">
          {r && r.days > 0 && focusStrat && (
            <div className="grid grid-cols-2 gap-3.5 xl:grid-cols-4">
              <div className="panel p-4">
                <div className="text-xs text-[var(--ink-2)]">{focusStrat.label} · 总收益</div>
                <div className={`mt-1 text-2xl font-bold tracking-tight mono ${dirClass(focusStrat.totalRet)}`}>
                  {pct(focusStrat.totalRet)}
                </div>
                <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
                  年化 {pct(focusStrat.cagr)} · 持仓 {Math.round(focusStrat.exposure * 100)}%
                </div>
              </div>
              <div className="panel p-4">
                <div className="text-xs text-[var(--ink-2)]">{focusStrat.label} · 最大回撤</div>
                <div className="mt-1 text-2xl font-bold tracking-tight mono down">{pct(focusStrat.maxDD)}</div>
                <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
                  估值档 {valBase ? pct(valBase.maxDD) : '—'} · 持有 {holdBase ? pct(holdBase.maxDD) : '—'}
                </div>
              </div>
              <div className="panel p-4">
                <div className="text-xs text-[var(--ink-2)]">估值档基线总收益</div>
                <div className={`mt-1 text-2xl font-bold tracking-tight mono ${dirClass(valBase?.totalRet ?? null)}`}>
                  {valBase ? pct(valBase.totalRet) : '—'}
                </div>
                <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
                  {valBase
                    ? focusStrat.totalRet >= valBase.totalRet
                      ? `跑赢估值档 ${pct(focusStrat.totalRet - valBase.totalRet)}`
                      : `落后估值档 ${pct(focusStrat.totalRet - valBase.totalRet)}`
                    : ''}
                </div>
              </div>
              <div className="panel p-4">
                <div className="text-xs text-[var(--ink-2)]">买入持有总收益</div>
                <div className={`mt-1 text-2xl font-bold tracking-tight mono ${dirClass(holdBase?.totalRet ?? null)}`}>
                  {holdBase ? pct(holdBase.totalRet) : '—'}
                </div>
                <div className="mt-1 text-[11.5px] text-[var(--ink-3)] mono">
                  温度计触发 {thermoSignals.length} 次信号
                </div>
              </div>
            </div>
          )}

          {/* 下划线式工作区 Tab 栏 */}
          <div className="tabs-bar" role="tablist">
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'equity'}
              className={`tab-btn ${tab === 'equity' ? 'on' : ''}`}
              onClick={() => setTab('equity')}
            >
              <TrendingUp className="h-4 w-4" />
              收益与策略拆解
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'signals'}
              className={`tab-btn ${tab === 'signals' ? 'on' : ''}`}
              onClick={() => setTab('signals')}
            >
              <FlaskConical className="h-4 w-4" />
              信号事后评估
              {thermoSignals.length > 0 && <span className="tag tag-muted">{thermoSignals.length}</span>}
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'robust'}
              className={`tab-btn ${tab === 'robust' ? 'on' : ''}`}
              onClick={() => setTab('robust')}
            >
              <ShieldCheck className="h-4 w-4" />
              稳健性与相关性
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'ablation'}
              className={`tab-btn ${tab === 'ablation' ? 'on' : ''}`}
              onClick={() => {
                setTab('ablation')
                if (!ablation && !ablationBusy) runAblation(params)
              }}
            >
              <Layers className="h-4 w-4" />
              维度消融诊断
            </button>
          </div>

          {r && r.days === 0 && <div className="panel empty">所选区间没有数据。</div>}

          {r && r.days > 0 && tab === 'equity' && (
            <>
              <section className="panel overflow-hidden">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] px-5 py-3">
                  <div className="flex items-center gap-2">
                    <strong className="text-sm font-semibold">策略收益对比</strong>
                    <span className="text-xs text-[var(--ink-2)]">
                      {r.from} ~ {r.to}，点击任意行高亮曲线并联动查看下方买卖点与仓位
                    </span>
                  </div>
                </div>
                <div className="table-scroll">
                  <table className="table">
                    <thead>
                      <tr>
                        <th>策略（点击切换检视）</th>
                        <th className="num">总收益</th>
                        <th className="num">年化</th>
                        <th className="num">最大回撤</th>
                        <th className="num">平均仓位</th>
                        <th className="num">调仓次数</th>
                      </tr>
                    </thead>
                    <tbody>
                      {r.strategies.map((s) => {
                        const m = STRAT_META[s.name]
                        const isSel = s.name === activeStratObj?.name
                        return (
                          <tr
                            key={s.name}
                            onClick={() => setSelectedStrat(s.name)}
                            className={isSel ? 'selected' : ''}
                          >
                            <td>
                              <div className="flex items-center gap-2.5">
                                <span
                                  className="h-2.5 w-2.5 shrink-0 rounded-full"
                                  style={{ background: `var(${m?.colorVar ?? '--ink-2'})` }}
                                />
                                <div>
                                  <div className="flex items-center gap-2">
                                    <span className={isSel ? 'font-semibold text-[var(--accent)]' : 'font-medium'}>
                                      {s.label}
                                    </span>
                                    {isSel && <span className="tag tag-cold">正在检视</span>}
                                  </div>
                                  {m && <div className="text-[11.5px] text-[var(--ink-3)]">{m.rule}</div>}
                                </div>
                              </div>
                            </td>
                            <td className={`num mono font-semibold ${dirClass(s.totalRet)}`}>{pct(s.totalRet)}</td>
                            <td className={`num mono ${dirClass(s.cagr)}`}>{pct(s.cagr)}</td>
                            <td className="num mono down">{pct(s.maxDD)}</td>
                            <td className="num mono">{Math.round(s.exposure * 100)}%</td>
                            <td className="num mono">{s.switches}</td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
                {equityOption && (
                  <div className="border-t border-[var(--line)] p-3">
                    <EChart option={equityOption} height={430} />
                  </div>
                )}
              </section>

              {inspector && activeStratObj && (
                <section className="panel overflow-hidden">
                  <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3">
                    <div>
                      <div className="flex flex-wrap items-center gap-2">
                        <span
                          className="h-2.5 w-2.5 rounded-full"
                          style={{ background: `var(${activeMeta.colorVar})` }}
                        />
                        <strong className="text-sm font-semibold">
                          {activeStratObj.label} · 买卖点与仓位变化
                        </strong>
                        <span className="tag tag-muted mono">
                          {activeStratObj.switches} 次调仓 · 平均仓位 {Math.round(activeStratObj.exposure * 100)}%
                        </span>
                      </div>
                      <p className="mt-1 mb-0 text-xs text-[var(--ink-2)]">
                        {activeMeta.rule}
                        {activeStratObj.name === 'layered' && '（▲▼ 为温度计加减仓信号，◆ 为估值档底仓切换点）'}
                        {activeStratObj.name === 'thermometer' && '（▲ 底部买入，▼ 顶部卖出，空心为估值昂贵时的观察级不入仓）'}
                      </p>
                    </div>

                    <div className="seg">
                      {r.strategies.map((s) => (
                        <button
                          key={s.name}
                          type="button"
                          className={s.name === activeStratObj.name ? 'on' : ''}
                          onClick={() => setSelectedStrat(s.name)}
                        >
                          {STRAT_META[s.name]?.short ?? s.label}
                        </button>
                      ))}
                    </div>
                  </div>
                  <div className="p-3">
                    <EChart option={inspector.option} height={inspector.hasPos ? 420 : 320} />
                  </div>
                </section>
              )}
            </>
          )}

          {r && r.days > 0 && tab === 'signals' && <BacktestSignals r={r} split={split} />}

          {tab === 'robust' && (
            <>
              <Robustness params={params} from={from} split={split} />
              {r && r.corr && r.corrNames && (
                <section className="panel overflow-hidden">
                  <div className="panel-head border-b border-[var(--line)]">
                    <strong>维度相关系数</strong>
                    <span className="muted text-xs">两两相关 &gt; 0.8 时可考虑合并或降权</span>
                  </div>
                  <div className="table-scroll">
                    <table className="table static corr">
                      <thead>
                        <tr>
                          <th></th>
                          {r.corrNames.map((n) => (
                            <th key={n} className="num">
                              {n}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {r.corr.map((row, i) => (
                          <tr key={i}>
                            <td className="font-medium">{r.corrNames![i]}</td>
                            {row.map((x, k) => (
                              <td
                                key={k}
                                className={`num mono ${x !== null && i !== k && Math.abs(x) > 0.8 ? 'warn-text' : ''}`}
                              >
                                {x === null ? '—' : x.toFixed(2)}
                              </td>
                            ))}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </section>
              )}
            </>
          )}
          {tab === 'ablation' && (
            <section className="panel overflow-hidden">
              <div className="panel-head border-b border-[var(--line)]">
                <Layers className="h-4 w-4 text-[var(--accent)]" />
                <strong>维度与规则开关消融诊断</strong>
                <span className="muted text-xs">
                  单维度、留一法与结构开关在样本内（&lt; {split}）及样本外（≥ {split}）的真实收益与回撤对照；点击“载入”可将对应方案直接填入左侧测试
                </span>
                <button
                  type="button"
                  className="btn btn-primary"
                  style={{ marginLeft: 'auto' }}
                  disabled={ablationBusy || !params}
                  onClick={() => runAblation(params)}
                >
                  <Play className="h-3.5 w-3.5" />
                  {ablationBusy ? '消融计算中…' : '重跑消融'}
                </button>
              </div>
              {ablation ? (
                <>
                  <div className="border-b border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-2 text-xs text-[var(--ink-2)] mono">
                    区间 {ablation.from} 起 · 样本外分界 {ablation.split} · 单边成本 {ablation.costBps} bps · 耗时 {ablation.elapsedMs} ms
                    {ablation.rows[0] && (
                      <>
                        {' '}
                        · 估值档基线 全区间 {pct(ablation.rows[0].all.valRet)}（样本外 {pct(ablation.rows[0].out.valRet)}）· 持有全区间{' '}
                        {pct(ablation.rows[0].all.holdRet)}（样本外 {pct(ablation.rows[0].out.holdRet)}）
                      </>
                    )}
                  </div>
                  {ablationOption && (
                    <div className="border-b border-[var(--line)] p-3">
                      <EChart option={ablationOption} height={270} />
                    </div>
                  )}
                  <div className="table-scroll">
                    <table className="table static">
                      <thead>
                        <tr>
                          <th>消融方案</th>
                          <th className="num nowrap">全区间·温度计</th>
                          <th className="num nowrap">全区间·分层</th>
                          <th className="num nowrap">全区间回撤(分层)</th>
                          <th className="num nowrap">样本内·温度计 / 分层</th>
                          <th className="num nowrap">样本外·温度计 / 分层</th>
                          <th className="num nowrap">买 / 卖信号</th>
                          <th></th>
                        </tr>
                      </thead>
                      <tbody>
                        {ablation.rows.map((row) => (
                          <tr key={row.key}>
                            <td>
                              <div className="font-medium text-[var(--ink)]">{row.label}</div>
                              <div className="text-[11px] text-[var(--ink-3)]">{row.desc}</div>
                            </td>
                            <td className={`num mono nowrap font-semibold ${dirClass(row.all.thermoRet)}`}>{pct(row.all.thermoRet)}</td>
                            <td className={`num mono nowrap font-semibold ${dirClass(row.all.layeredRet)}`}>{pct(row.all.layeredRet)}</td>
                            <td className="num mono nowrap down">{pct(row.all.layeredDD)}</td>
                            <td className="num mono small nowrap">
                              <span className={dirClass(row.in.thermoRet)}>{pct(row.in.thermoRet)}</span> /{' '}
                              <span className={dirClass(row.in.layeredRet)}>{pct(row.in.layeredRet)}</span>
                            </td>
                            <td className="num mono small nowrap">
                              <span className={dirClass(row.out.thermoRet)}>{pct(row.out.thermoRet)}</span> /{' '}
                              <span className={dirClass(row.out.layeredRet)}>{pct(row.out.layeredRet)}</span>
                            </td>
                            <td className="num mono small nowrap">
                              {row.all.buys} / {row.all.sells}
                            </td>
                            <td className="num">
                              {row.key !== 'base' && (
                                <button
                                  type="button"
                                  className="btn btn-ghost"
                                  onClick={() => {
                                    const next = structuredClone(row.params)
                                    setParams(next)
                                    setTab('equity')
                                    run(next)
                                    setNotice(`已载入消融方案「${row.label}」并重跑回测，未覆盖线上生效参数。`)
                                  }}
                                >
                                  载入
                                </button>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                </>
              ) : (
                <div className="empty">{ablationBusy ? '正在对 10 组维度与规则消融方案执行全量 + 样本内外回测…' : '点击右上角“重跑消融”开始诊断。'}</div>
              )}
            </section>
          )}
        </div>
      </div>
    </div>
  )
}
