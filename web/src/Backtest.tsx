import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, type BacktestResp, type BtGroup, type FullParams, type HStat, type ParamsResp } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { asMarkers, priceWithSignals } from './charts'
import { fmt, pct, TIER_TEXT } from './temperature'

type Field = { path: string; label: string; step?: number; hint?: string }
type Group = { title: string; fields: Field[] }

// 参数表单定义：path 为参数 JSON 中的路径（signal.xxx 为状态机参数，weights.N 为维度权重）
const SCHEMA: Group[] = [
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
    title: '通用',
    fields: [
      { path: 'signal.minExtremeDims', label: '武装需极端维度数' },
      { path: 'signal.divergenceGap', label: '背离幅度' },
      { path: 'signal.armTimeout', label: '武装超时（日）' },
      { path: 'signal.disarmMid', label: '武装失效中线' },
    ],
  },
]

function getPath(p: FullParams, path: string): number {
  return path.split('.').reduce<unknown>((o, k) => (o as Record<string, unknown>)?.[k], p) as number
}

function setPath(p: FullParams, path: string, v: number): FullParams {
  const next = structuredClone(p)
  const keys = path.split('.')
  let o = next as unknown as Record<string, unknown>
  for (const k of keys.slice(0, -1)) o = o[k] as Record<string, unknown>
  o[keys[keys.length - 1]] = v
  return next
}

const SOURCE_TEXT: Record<string, string> = { thermometer: '温度计', valuation: '估值档', ma200: '200 日均线' }
const PERIOD_TEXT: Record<string, string> = { all: '全部', in: '样本内', out: '样本外' }

/** A 股习惯：涨红跌绿。信号是否有效看胜率（底部信号以上涨为胜，顶部信号以下跌为胜）。 */
function dirClass(x: number | null) {
  if (x === null || Math.abs(x) < 0.0005) return ''
  return x > 0 ? 'up' : 'down'
}

function StatCells({ h }: { h: HStat }) {
  return (
    <>
      <td className={`num mono ${dirClass(h.median)}`}>{pct(h.median)}</td>
      <td className="num mono muted">{h.winRate === null ? '—' : `${Math.round(h.winRate * 100)}%`}</td>
    </>
  )
}

export default function Backtest() {
  const [meta, setMeta] = useState<ParamsResp | null>(null)
  const [params, setParams] = useState<FullParams | null>(null)
  const [from, setFrom] = useState('2014-01-01')
  const [split, setSplit] = useState('2021-01-01')
  const [res, setRes] = useState<BacktestResp | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [name, setName] = useState('')

  const run = useCallback(async (p: FullParams | null) => {
    setBusy(true)
    setError('')
    try {
      setRes(await api.backtest(p, { from, split }))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }, [from, split])

  const loadMeta = useCallback(() => {
    return api.params().then((m) => {
      setMeta(m)
      const active = m.sets.find((s) => s.active)
      setParams((cur) => cur ?? structuredClone(active?.params ?? m.defaults))
      return active?.params ?? m.defaults
    })
  }, [])

  useEffect(() => {
    loadMeta().then((p) => run(p), (e) => setError(e.message))
    // 只在进入页面时自动运行一次
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
  const equityOption = useMemo<EChartsOption | null>(() => {
    if (!r || r.dates.length === 0) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line')
    const colors: Record<string, string> = {
      thermometer: cssVar('--accent'), valuation: cssVar('--t-hot-1'), ma200: cssVar('--dim-2'), hold: ink,
    }
    return {
      animation: false,
      grid: { left: 56, right: 24, top: 36, bottom: 56 },
      tooltip: { trigger: 'axis', valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(3) : String(v)) },
      legend: { top: 0, left: 0, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
      xAxis: { type: 'category', data: r.dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      yAxis: { type: 'value', scale: true, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      dataZoom: [{ type: 'inside' }, { type: 'slider', height: 18, bottom: 8, borderColor: line, textStyle: { color: ink } }],
      series: r.strategies.map((s) => ({
        name: s.label, type: 'line', data: s.equity, showSymbol: false,
        lineStyle: { width: s.name === 'thermometer' ? 2 : 1.2, color: colors[s.name] }, itemStyle: { color: colors[s.name] },
      })),
    }
  }, [r])

  const priceOption = useMemo(() => (r ? priceWithSignals(r.dates, r.close, asMarkers(r.events)) : null), [r])

  const groups = r?.groups ?? []
  const horizons = r?.config.horizons ?? []
  const thermoSignals = (r?.signals ?? []).filter((s) => s.source === 'thermometer').slice().reverse()

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>回测</h1>
          <p className="sub">
            调整参数后在本地数据上重算并评估信号，不会改动线上结果；满意后可保存为参数集并设为生效。
            {res && <span className="muted"> 本次耗时 {res.elapsedMs} ms。</span>}
          </p>
        </div>
      </div>

      {error && <div className="error" role="alert">{error}</div>}
      {notice && <div className="ok-note" role="status">{notice}</div>}

      <div className="bt-layout">
        <aside className="panel bt-form">
          <div className="bt-form-head">
            <strong>参数</strong>
            {res && (
              <span className={`tag ${dirty ? 'tag-warn' : 'tag-ok'}`}>{dirty ? `试验参数 ${res.paramsVersion}` : '与生效参数一致'}</span>
            )}
          </div>
          <div className="bt-range">
            <label className="field-inline"><span>回测起点</span><input type="date" value={from} onChange={(e) => setFrom(e.target.value)} /></label>
            <label className="field-inline"><span>样本外起点</span><input type="date" value={split} onChange={(e) => setSplit(e.target.value)} /></label>
          </div>
          {params && SCHEMA.map((g) => (
            <fieldset key={g.title} className="bt-group">
              <legend>{g.title}</legend>
              {g.fields.map((f) => (
                <label key={f.path} className="field-inline" title={f.hint}>
                  <span>{f.label}</span>
                  <input
                    type="number"
                    step={f.step ?? 1}
                    value={getPath(params, f.path) ?? ''}
                    onChange={(e) => {
                      const v = e.target.valueAsNumber
                      if (!Number.isNaN(v)) setParams(setPath(params, f.path, v))
                    }}
                  />
                </label>
              ))}
            </fieldset>
          ))}
          <div className="bt-actions">
            <button className="btn btn-primary" disabled={busy || !params} onClick={() => run(params)}>
              {busy ? '运行中…' : '运行回测'}
            </button>
            <button className="btn" disabled={busy || !active} onClick={() => { const p = structuredClone(active!.params); setParams(p); run(p) }}>
              恢复生效参数
            </button>
            <button className="btn btn-ghost" disabled={busy || !meta} onClick={() => setParams(structuredClone(meta!.defaults))}>
              填入默认值
            </button>
          </div>
          <div className="bt-save">
            <input placeholder="参数集名称" value={name} maxLength={40} onChange={(e) => setName(e.target.value)} />
            <button className="btn" disabled={!name.trim()} onClick={() => save(false)}>保存</button>
            <button className="btn" disabled={!name.trim()} onClick={() => { if (confirm('设为生效后，总览、信号与后续推送都将按这组参数计算。确定？')) save(true) }}>
              保存并生效
            </button>
          </div>
          {meta && meta.sets.length > 1 && (
            <div className="bt-sets">
              <div className="muted">已保存的参数集</div>
              {meta.sets.map((s) => (
                <button key={s.version} className="link" onClick={() => setParams(structuredClone(s.params))}>
                  {s.name || s.version} <span className="muted mono">{s.version}</span>{s.active && ' · 生效中'}
                </button>
              ))}
            </div>
          )}
        </aside>

        <div className="bt-results">
          {r && r.days > 0 && (
            <>
              <section className="panel">
                <div className="panel-head">
                  <strong>模拟持仓</strong>
                  <span className="muted">{r.from} ~ {r.to}，信号次日生效，不计交易成本</span>
                </div>
                <table className="table static">
                  <thead>
                    <tr><th>策略</th><th className="num">总收益</th><th className="num">年化</th><th className="num">最大回撤</th><th className="num">持仓占比</th><th className="num">开平仓</th></tr>
                  </thead>
                  <tbody>
                    {r.strategies.map((s) => (
                      <tr key={s.name} className={s.name === 'thermometer' ? 'emph' : ''}>
                        <td>{s.label}</td>
                        <td className="num mono">{pct(s.totalRet)}</td>
                        <td className="num mono">{pct(s.cagr)}</td>
                        <td className="num mono">{pct(s.maxDD)}</td>
                        <td className="num mono">{Math.round(s.exposure * 100)}%</td>
                        <td className="num mono">{s.switches}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {equityOption && <EChart option={equityOption} height={300} />}
              </section>

              <section className="panel">
                <div className="panel-head">
                  <strong>信号事后表现</strong>
                  <span className="muted">指数涨跌幅中位数（红涨绿跌）/ 胜率：底部信号以上涨为胜，顶部信号以下跌为胜。观察级信号不计入。</span>
                </div>
                <div className="table-scroll">
                  <table className="table static stats">
                    <thead>
                      <tr>
                        <th>信号</th><th>区间</th><th className="num">次数</th>
                        {horizons.map((h) => <th key={h} className="num" colSpan={2}>{h} 日</th>)}
                        <th className="num">MAE 中位</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr className="base">
                        <td>任意一天买入（基准）</td><td>全部</td><td className="num mono">—</td>
                        {r.baseRate.map((h, i) => <StatCells key={i} h={h} />)}
                        <td className="num mono">—</td>
                      </tr>
                      {groups.map((g: BtGroup, i) => (
                        <tr key={i} className={g.period === 'all' ? 'group-first' : 'sub'}>
                          <td>{g.period === 'all' ? `${SOURCE_TEXT[g.source]} · ${g.side > 0 ? '底部 / 买' : '顶部 / 卖'}` : ''}</td>
                          <td>{PERIOD_TEXT[g.period]}</td>
                          <td className="num mono">{g.n}</td>
                          {g.horizons.map((h, k) => <StatCells key={k} h={h} />)}
                          <td className="num mono">{pct(g.maeMedian)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <p className="foot muted">
                  样本外以 {split} 为界。样本量小（尤其样本内），差异可能只是偶然；参数改动后应同时看样本外是否仍然成立。
                </p>
              </section>

              {priceOption && (
                <section className="panel">
                  <div className="panel-head"><strong>信号位置</strong><span className="muted">▲ 底部，▼ 顶部，空心为观察级</span></div>
                  <EChart option={priceOption} height={340} />
                </section>
              )}

              <section className="panel">
                <div className="panel-head"><strong>温度计信号明细</strong><span className="muted">{thermoSignals.length} 个</span></div>
                <div className="table-scroll">
                  <table className="table static">
                    <thead>
                      <tr>
                        <th>日期</th><th>信号</th><th className="num">收盘</th><th className="num">T_s</th><th>估值</th>
                        {horizons.map((h) => <th key={h} className="num">{h} 日</th>)}
                        <th className="num">MAE</th>
                      </tr>
                    </thead>
                    <tbody>
                      {thermoSignals.map((s, i) => (
                        <tr key={i} title={s.reason}>
                          <td className="mono">{s.date}</td>
                          <td>
                            <span className={`tag ${s.side > 0 ? 'tag-cold' : 'tag-hot'}`}>{s.side > 0 ? '底部' : '顶部'}</span>
                            {s.observe && <span className="tag tag-muted" style={{ marginLeft: 4 }}>观察</span>}
                          </td>
                          <td className="num mono">{fmt(s.close, 2)}</td>
                          <td className="num mono">{fmt(s.tS)}</td>
                          <td>{s.tier ? TIER_TEXT[s.tier] : '—'}</td>
                          {s.rets.map((x, k) => <td key={k} className={`num mono ${dirClass(x)}`}>{pct(x)}</td>)}
                          <td className="num mono">{pct(s.mae)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>

              {r.corr && r.corrNames && (
                <section className="panel">
                  <div className="panel-head"><strong>维度相关系数</strong><span className="muted">两两相关 &gt; 0.8 时考虑合并</span></div>
                  <table className="table static corr">
                    <thead><tr><th></th>{r.corrNames.map((n) => <th key={n} className="num">{n}</th>)}</tr></thead>
                    <tbody>
                      {r.corr.map((row, i) => (
                        <tr key={i}>
                          <td>{r.corrNames![i]}</td>
                          {row.map((x, k) => <td key={k} className={`num mono ${x !== null && i !== k && Math.abs(x) > 0.8 ? 'warn-text' : ''}`}>{x === null ? '—' : x.toFixed(2)}</td>)}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </section>
              )}
            </>
          )}
          {r && r.days === 0 && <div className="panel empty">所选区间没有数据。</div>}
        </div>
      </div>
    </div>
  )
}
