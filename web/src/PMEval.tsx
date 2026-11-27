import { useCallback, useEffect, useMemo, useState } from 'react'
import { FlaskConical, Play } from 'lucide-react'
import { api, type PMBucketStat, type PMEvalConfig, type PMEvalReport } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { useTheme } from './theme'

const DEFAULT_CFG: PMEvalConfig = {
  days: 14,
  minEdge: 0.03,
  askMin: 0,
  askMax: 0,
  kSrc: '',
  series: '',
  firstPerMarket: true,
  execMode: 'taker',
  makerOffset: 0,
  delayMs: 1000,
  heartbeatMs: 15000,
  clusterMin: 15,
  initialCash: 1000,
  sizing: 'fixed',
  orderUsd: 20,
  kellyFrac: 0.25,
  maxAssetUsd: 100,
  useFairLimit: true,
}

const pct = (x: number) => `${(x * 100).toFixed(1)}%`
const money = (x: number) => `${x < 0 ? '−' : ''}$${Math.abs(x).toFixed(2)}`
const signedMoney = (x: number) => (
  <span className={x > 0 ? 'us-up' : x < 0 ? 'us-down' : ''}>
    {x > 0 ? '+' : ''}
    {money(x)}
  </span>
)

const KSRC_LABEL: Record<string, string> = { exact: '精确起始价', spot: '2.5s 现价估计', bar: '1m K 线估计' }
const SKIP_LABEL: Record<string, string> = {
  stale: '心跳超时',
  no_book: '无盘口快照',
  risk_cap: '单资产敞口上限',
  no_fill: '限价/深度不足',
  unresolved: '未结算',
}

export function EvalPanel({ seriesList }: { seriesList: string[] }) {
  const [cfg, setCfg] = useState<PMEvalConfig>(DEFAULT_CFG)
  const [rep, setRep] = useState<PMEvalReport | null>(null)
  const [elapsedMs, setElapsedMs] = useState(0)
  const [attrDim, setAttrDim] = useState<'edge' | 'ask' | 'tau' | 'series'>('edge')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const { theme } = useTheme()

  const run = useCallback(
    (c: PMEvalConfig) => {
      setBusy(true)
      setError('')
      api
        .pmEval(c)
        .then(
          (r) => {
            setRep(r.report)
            setElapsedMs(r.elapsedMs)
          },
          (e: Error) => setError(e.message),
        )
        .finally(() => setBusy(false))
    },
    [],
  )

  useEffect(() => {
    run(DEFAULT_CFG)
  }, [run])

  const calibOption = useMemo<EChartsOption | null>(() => {
    if (!rep) return null
    const pts = rep.calibration.filter((b) => b.n > 0)
    if (pts.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const accent = cssVar('--accent')
    return {
      animation: false,
      tooltip: {
        trigger: 'item',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
      },
      grid: { left: 48, right: 20, top: 20, bottom: 36 },
      xAxis: {
        type: 'value',
        name: '模型概率',
        nameLocation: 'middle',
        nameGap: 22,
        min: 0,
        max: 1,
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${Math.round(v * 100)}%` },
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
      },
      yAxis: {
        type: 'value',
        name: '实际胜率',
        min: 0,
        max: 1,
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${Math.round(v * 100)}%` },
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
      },
      series: [
        {
          name: '理想校准线',
          type: 'line',
          data: [
            [0, 0],
            [1, 1],
          ],
          showSymbol: false,
          lineStyle: { color: ink, width: 1, type: 'dashed' },
          silent: true,
        },
        {
          name: '实测分桶',
          type: 'line',
          data: pts.map((b) => ({
            value: [b.meanP, b.actualRate],
            name: `${b.label}（N=${b.n}）`,
          })),
          symbolSize: 8,
          lineStyle: { color: accent, width: 2 },
          itemStyle: { color: accent },
        },
      ],
    }
  }, [rep, theme])

  const curveOption = useMemo<EChartsOption | null>(() => {
    if (!rep || rep.replay.curve.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const ok = cssVar('--ok')
    const bad = cssVar('--bad')
    const color = rep.replay.netPnl >= 0 ? ok : bad
    const pad = (n: number) => String(n).padStart(2, '0')
    const fmt = (ms: number) => {
      const d = new Date(ms)
      return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
    }
    return {
      animation: false,
      tooltip: {
        trigger: 'axis',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
      },
      grid: { left: 60, right: 20, top: 20, bottom: 32 },
      xAxis: {
        type: 'category',
        data: rep.replay.curve.map((p) => fmt(p.t)),
        axisLabel: { color: ink, fontSize: 11 },
        axisLine: { lineStyle: { color: line } },
      },
      yAxis: {
        type: 'value',
        scale: true,
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `$${v}` },
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
      },
      series: [
        {
          name: '回放权益',
          type: 'line',
          data: rep.replay.curve.map((p) => p.equity),
          showSymbol: false,
          lineStyle: { color, width: 2 },
          areaStyle: { color, opacity: 0.12 },
        },
      ],
    }
  }, [rep, theme])

  // 分桶归因柱状图（理论 1 股累计盈亏柱 + 实际胜率 vs 模型均价双折线）
  const attrOption = useMemo<EChartsOption | null>(() => {
    if (!rep) return null
    const raw =
      attrDim === 'edge'
        ? (rep.byEdge ?? [])
        : attrDim === 'ask'
          ? (rep.byAsk ?? [])
          : attrDim === 'tau'
            ? (rep.byTau ?? [])
            : (rep.bySeries ?? [])
    if (raw.length === 0) return null
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const ok = cssVar('--us-up') || cssVar('--ok')
    const bad = cssVar('--us-down') || cssVar('--bad')
    const accent = cssVar('--accent')
    const warn = cssVar('--warn')
    const labels = raw.map((b) => (attrDim === 'series' ? b.key.replace('-up-or-down-', ' ').toUpperCase() : b.key))
    return {
      animation: false,
      grid: { left: 56, right: 52, top: 36, bottom: 40 },
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
      },
      legend: { top: 0, left: 0, textStyle: { color: ink, fontSize: 12 }, itemWidth: 12, itemHeight: 8 },
      xAxis: {
        type: 'category',
        data: labels,
        axisLine: { lineStyle: { color: line } },
        axisLabel: { color: ink, fontSize: 11, interval: 0 },
      },
      yAxis: [
        {
          type: 'value',
          name: '1股理论盈亏 ($)',
          nameTextStyle: { color: ink, fontSize: 11 },
          splitLine: { lineStyle: { color: line, type: 'dashed' } },
          axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(1)}` },
        },
        {
          type: 'value',
          name: '胜率 / 均价',
          min: 0,
          max: 100,
          nameTextStyle: { color: ink, fontSize: 11 },
          splitLine: { show: false },
          axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v}%` },
        },
      ],
      series: [
        {
          name: '理论1股累计盈亏',
          type: 'bar',
          barMaxWidth: 32,
          data: raw.map((b) => ({
            value: +b.theoreticalPnL.toFixed(2),
            itemStyle: {
              color: b.theoreticalPnL >= 0 ? ok : bad,
              borderRadius: b.theoreticalPnL >= 0 ? [4, 4, 0, 0] : [0, 0, 4, 4],
            },
          })),
        },
        {
          name: '实际胜率 (%)',
          type: 'line',
          yAxisIndex: 1,
          data: raw.map((b) => (b.resolved > 0 ? +(b.winRate * 100).toFixed(1) : null)),
          symbolSize: 7,
          lineStyle: { color: accent, width: 2 },
          itemStyle: { color: accent },
        },
        {
          name: '模型均价 (%)',
          type: 'line',
          yAxisIndex: 1,
          data: raw.map((b) => +(b.avgModelP * 100).toFixed(1)),
          symbolSize: 6,
          lineStyle: { color: warn, width: 1.5, type: 'dashed' },
          itemStyle: { color: warn },
        },
      ],
    }
  }, [rep, attrDim, theme])

  const inputCls =
    'h-8 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-2.5 text-xs mono text-[var(--ink)] focus:border-[var(--accent)] focus:outline-none'

  return (
    <div className="flex flex-col gap-4">
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <FlaskConical className="h-4 w-4 text-[var(--accent)]" />
          <strong>信号评估与历史盘口回放</strong>
          <span className="muted">
            按“信号 + 延迟”后下一张本地盘口快照逐档吃单，以 pm_spot 作心跳防断流
            {elapsedMs > 0 && ` · 耗时 ${elapsedMs}ms`}
          </span>
          <button className="btn btn-primary" style={{ marginLeft: 'auto' }} disabled={busy} onClick={() => run(cfg)}>
            <Play className="h-3.5 w-3.5" />
            {busy ? '回放中…' : '运行评估'}
          </button>
        </div>

        <div className="grid grid-cols-2 gap-3 p-4 sm:grid-cols-4 lg:grid-cols-6">
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>回溯天数</span>
            <input type="number" className={inputCls} value={cfg.days ?? 14} min={0} onChange={(e) => setCfg({ ...cfg, days: e.target.valueAsNumber || 0 })} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>最小 Edge (¢)</span>
            <input
              type="number"
              className={inputCls}
              step={0.5}
              value={Math.round(cfg.minEdge * 1000) / 10}
              onChange={(e) => setCfg({ ...cfg, minEdge: (e.target.valueAsNumber || 0) / 100 })}
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>执行延迟 (ms)</span>
            <input type="number" className={inputCls} step={250} min={0} value={cfg.delayMs} onChange={(e) => setCfg({ ...cfg, delayMs: e.target.valueAsNumber || 0 })} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>起始价来源</span>
            <select className={inputCls} value={cfg.kSrc} onChange={(e) => setCfg({ ...cfg, kSrc: e.target.value })}>
              <option value="">全部</option>
              <option value="exact">精确起始价</option>
              <option value="spot">现价估计</option>
              <option value="bar">1m K 线估计</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>系列过滤</span>
            <select className={inputCls} value={cfg.series} onChange={(e) => setCfg({ ...cfg, series: e.target.value })}>
              <option value="">全部系列</option>
              {seriesList.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>仓位模式</span>
            <select className={inputCls} value={cfg.sizing} onChange={(e) => setCfg({ ...cfg, sizing: e.target.value as 'fixed' | 'kelly' })}>
              <option value="fixed">固定金额</option>
              <option value="kelly">分数凯利</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>{cfg.sizing === 'kelly' ? '单笔上限 ($)' : '单笔金额 ($)'}</span>
            <input type="number" className={inputCls} value={cfg.orderUsd} min={5} onChange={(e) => setCfg({ ...cfg, orderUsd: e.target.valueAsNumber || 20 })} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>单资产敞口上限 ($)</span>
            <input type="number" className={inputCls} value={cfg.maxAssetUsd} min={0} onChange={(e) => setCfg({ ...cfg, maxAssetUsd: e.target.valueAsNumber || 0 })} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>执行方式</span>
            <select className={inputCls} value={cfg.execMode} onChange={(e) => setCfg({ ...cfg, execMode: e.target.value as 'taker' | 'maker' })}>
              <option value="taker">吃单（Taker 扣费）</option>
              <option value="maker">被动挂单（Maker 0费）</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>卖一区间过滤 (¢)</span>
            <div className="flex gap-1">
              <input
                type="number"
                className={`${inputCls} w-full`}
                placeholder="最小"
                value={cfg.askMin ? Math.round(cfg.askMin * 100) : ''}
                onChange={(e) => setCfg({ ...cfg, askMin: (e.target.valueAsNumber || 0) / 100 })}
              />
              <input
                type="number"
                className={`${inputCls} w-full`}
                placeholder="最大"
                value={cfg.askMax ? Math.round(cfg.askMax * 100) : ''}
                onChange={(e) => setCfg({ ...cfg, askMax: (e.target.valueAsNumber || 0) / 100 })}
              />
            </div>
          </label>
          <label className="flex items-center gap-2 pt-4 text-xs text-[var(--ink)]">
            <input type="checkbox" checked={cfg.firstPerMarket} onChange={(e) => setCfg({ ...cfg, firstPerMarket: e.target.checked })} />
            每市场仅首条信号
          </label>
          <label className="flex items-center gap-2 pt-4 text-xs text-[var(--ink)]">
            <input type="checkbox" checked={cfg.useFairLimit} onChange={(e) => setCfg({ ...cfg, useFairLimit: e.target.checked })} />
            公允价限价保护
          </label>
        </div>
      </section>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      {rep && (
        <>
          {/* 汇总 KPI 条 */}
          <div className="grid grid-cols-2 gap-3.5 md:grid-cols-3 xl:grid-cols-6">
            <div className="panel p-3.5">
              <div className="text-xs text-[var(--ink-2)]">有效信号 / 市场 / 聚类</div>
              <div className="mt-1 text-lg font-bold mono">
                {rep.filteredSignals} <span className="text-xs font-normal text-[var(--ink-3)]">/ {rep.uniqueMarkets} / {rep.clusters}</span>
              </div>
              <div className="mt-0.5 text-[11px] text-[var(--ink-3)] mono">原始 {rep.rawSignals} 条 · {rep.config.clusterMin}m 聚类</div>
            </div>
            <div className="panel p-3.5">
              <div className="text-xs text-[var(--ink-2)]">信号胜率 vs 模型均价</div>
              <div className="mt-1 text-lg font-bold mono">
                {pct(rep.overall.winRate)} <span className="text-xs font-normal text-[var(--ink-3)]">vs {pct(rep.overall.avgModelP)}</span>
              </div>
              <div className="mt-0.5 text-[11px] text-[var(--ink-3)] mono">Brier {rep.overall.brier.toFixed(4)} · 已结算 {rep.overall.resolved}</div>
            </div>
            <div className="panel p-3.5">
              <div className="text-xs text-[var(--ink-2)]">回放净盈亏</div>
              <div className="mt-1 text-lg font-bold mono">{signedMoney(rep.replay.netPnl)}</div>
              <div className="mt-0.5 text-[11px] text-[var(--ink-3)] mono">
                成交 {rep.replay.executed} 笔 · 胜率 {pct(rep.replay.winRate)}
              </div>
            </div>
            <div className="panel p-3.5">
              <div className="text-xs text-[var(--ink-2)]">成交额 ROI / 手续费</div>
              <div className="mt-1 text-lg font-bold mono">
                <span className={rep.replay.roiOnVolume >= 0 ? 'us-up' : 'us-down'}>{(rep.replay.roiOnVolume * 100).toFixed(2)}%</span>
              </div>
              <div className="mt-0.5 text-[11px] text-[var(--ink-3)] mono">
                额 {money(rep.replay.totalGross)} · 费 {money(rep.replay.totalFees)}
              </div>
            </div>
            <div className="panel p-3.5">
              <div className="text-xs text-[var(--ink-2)]">最大回撤 / 平均滑点</div>
              <div className="mt-1 text-lg font-bold mono">
                <span className="us-down">{(rep.replay.maxDD * 100).toFixed(2)}%</span>
              </div>
              <div className="mt-0.5 text-[11px] text-[var(--ink-3)] mono">
                滑点 {rep.replay.avgSlippage >= 0 ? '+' : ''}
                {(rep.replay.avgSlippage * 100).toFixed(2)}¢
              </div>
            </div>
            <div className="panel p-3.5">
              <div className="text-xs text-[var(--ink-2)]">聚类夏普 / 跳过笔数</div>
              <div className="mt-1 text-lg font-bold mono">{rep.replay.clusterSharpe.toFixed(2)}</div>
              <div className="mt-0.5 text-[11px] text-[var(--ink-3)] mono">
                超时 {rep.replay.skippedStale} · 缺簿 {rep.replay.skippedNoBook} · 风控 {rep.replay.skippedRisk} · 未成 {rep.replay.skippedNoFill}
              </div>
            </div>
          </div>

          {/* 校准图 + 回放净值曲线 */}
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <section className="panel p-4">
              <div className="mb-2 flex items-center justify-between">
                <strong className="text-sm">概率校准曲线（Calibration）</strong>
                <span className="text-xs text-[var(--ink-2)]">点越贴近对角虚线说明概率估计越准</span>
              </div>
              {calibOption ? <EChart option={calibOption} height={240} /> : <div className="empty">已结算样本不足</div>}
            </section>
            <section className="panel p-4">
              <div className="mb-2 flex items-center justify-between">
                <strong className="text-sm">历史盘口回放权益曲线</strong>
                <span className="text-xs text-[var(--ink-2)] mono">
                  初始 {money(rep.replay.initialCash)} → 期末 {money(rep.replay.finalEquity)}
                </span>
              </div>
              {curveOption ? <EChart option={curveOption} height={240} /> : <div className="empty">无成交记录</div>}
            </section>
          </div>

          {/* 分桶归因可视化柱状图 */}
          {attrOption && (
            <section className="panel overflow-hidden">
              <div className="panel-head border-b border-[var(--line)]">
                <strong className="text-sm font-semibold">分桶归因对比图（绿盈红亏）</strong>
                <span className="text-xs text-[var(--ink-2)]">
                  柱状为理论 1 股累计盈亏，折线对比实际胜率与模型均价（快速识别过自信区间）
                </span>
                <div className="seg ml-auto">
                  {(
                    [
                      ['edge', '按 Edge 区间'],
                      ['ask', '按卖一价格'],
                      ['tau', '按剩余时间 τ'],
                      ['series', '按系列'],
                    ] as const
                  ).map(([k, lbl]) => (
                    <button key={k} className={attrDim === k ? 'on' : ''} onClick={() => setAttrDim(k)}>
                      {lbl}
                    </button>
                  ))}
                </div>
              </div>
              <div className="p-3">
                <EChart option={attrOption} height={260} />
              </div>
            </section>
          )}

          {/* 分层表：Edge 分桶 / 卖一价格分桶 / 起始价来源 / 剩余时间 / 系列 */}
          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            <BucketTable title="按 Edge 预期收益分桶（定位有效阈值）" rows={rep.byEdge ?? []} />
            <BucketTable title="按卖一价格分桶（识别 40–60¢ 币抛区）" rows={rep.byAsk ?? []} />
          </div>
          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            <BucketTable title="按起始价来源拆分" rows={rep.byKSrc} labelOf={(k) => KSRC_LABEL[k] ?? k} />
            <BucketTable title="按剩余时间 τ 拆分" rows={rep.byTau} />
          </div>
          <BucketTable title="按系列拆分" rows={rep.bySeries} />

          {/* 回放成交明细 */}
          <section className="panel overflow-hidden">
            <div className="panel-head border-b border-[var(--line)]">
              <strong>回放交易明细</strong>
              <span className="muted">最近 {rep.replay.trades.length} 条</span>
            </div>
            <div className="scroll-y">
              <table className="table static">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>市场</th>
                    <th>方向</th>
                    <th>起始价</th>
                    <th className="num">τ</th>
                    <th className="num">模型 P</th>
                    <th className="num">信号卖一</th>
                    <th className="num">回放均价</th>
                    <th className="num">股数</th>
                    <th className="num">盈亏</th>
                    <th>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {rep.replay.trades.map((tr) => (
                    <tr key={`${tr.id}-${tr.t}`}>
                      <td className="mono nowrap small">{new Date(tr.t).toLocaleString('zh-CN', { hour12: false }).slice(5)}</td>
                      <td className="mono small">
                        <a href={`#/predict/${tr.slug}`} className="text-[var(--accent)] no-underline hover:underline">
                          {tr.slug}
                        </a>
                      </td>
                      <td>
                        <span className={`tag ${tr.outcome === 0 ? 'tag-hot' : 'tag-cold'}`}>{tr.outcome === 0 ? 'Up' : 'Down'}</span>
                      </td>
                      <td className="small muted">{KSRC_LABEL[tr.kSrc] ?? tr.kSrc}</td>
                      <td className="num mono small">{tr.tau}s</td>
                      <td className="num mono small">{pct(tr.p)}</td>
                      <td className="num mono small">{(tr.signalAsk * 100).toFixed(1)}¢</td>
                      <td className="num mono small">{tr.status === 'filled' ? `${(tr.fillAvg * 100).toFixed(1)}¢` : '—'}</td>
                      <td className="num mono small">{tr.status === 'filled' ? tr.shares.toFixed(1) : '—'}</td>
                      <td className="num mono small">{tr.status === 'filled' ? signedMoney(tr.pnl) : '—'}</td>
                      <td className="small">
                        {tr.status === 'filled' ? (
                          <span className={`tag ${tr.won ? 'tag-ok' : 'tag-bad'}`}>{tr.won ? '赢' : '输'}</span>
                        ) : (
                          <span className="tag tag-muted">{SKIP_LABEL[tr.skipReason] ?? tr.skipReason}</span>
                        )}
                      </td>
                    </tr>
                  ))}
                  {rep.replay.trades.length === 0 && (
                    <tr>
                      <td colSpan={11} className="muted">
                        没有匹配的信号记录。
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </section>
        </>
      )}
    </div>
  )
}

function BucketTable({ title, rows, labelOf }: { title: string; rows: PMBucketStat[]; labelOf?: (k: string) => string }) {
  return (
    <section className="panel overflow-hidden">
      <div className="panel-head border-b border-[var(--line)]">
        <strong>{title}</strong>
      </div>
      <div className="table-scroll">
        <table className="table static">
          <thead>
            <tr>
              <th>分组</th>
              <th className="num">信号数</th>
              <th className="num">已结算</th>
              <th className="num">胜率</th>
              <th className="num">模型均价</th>
              <th className="num">均卖一</th>
              <th className="num">Brier</th>
              <th className="num">理论1股累计</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((b) => (
              <tr key={b.key}>
                <td className="mono font-medium">{labelOf ? labelOf(b.key) : b.key}</td>
                <td className="num mono">{b.n}</td>
                <td className="num mono">{b.resolved}</td>
                <td className="num mono">{b.resolved > 0 ? pct(b.winRate) : '—'}</td>
                <td className="num mono">{pct(b.avgModelP)}</td>
                <td className="num mono">{(b.avgAsk * 100).toFixed(1)}¢</td>
                <td className="num mono">{b.resolved > 0 ? b.brier.toFixed(4) : '—'}</td>
                <td className="num mono">
                  <span className={b.theoreticalPnL > 0 ? 'us-up' : b.theoreticalPnL < 0 ? 'us-down' : ''}>
                    {b.theoreticalPnL >= 0 ? '+' : ''}
                    {b.theoreticalPnL.toFixed(2)}
                  </span>
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={8} className="muted">
                  暂无数据
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}
