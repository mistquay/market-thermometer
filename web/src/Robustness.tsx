import { Fragment, useState } from 'react'
import { api, type FullParams, type RobustOutcome, type RobustResp } from './api'
import { pct } from './temperature'

const strat = (o: RobustOutcome | undefined, name: string) => o?.strategies.find((s) => s.name === name)

function dir(x: number | undefined) {
  if (x === undefined || Math.abs(x) < 0.0005) return ''
  return x > 0 ? 'up' : 'down'
}

/** 稳健性检验：参数 ±20% 单参数扰动 + 分年度表现。只展示，不排序、不挑参数。 */
export default function Robustness({ params, from, split }: { params: FullParams | null; from: string; split: string }) {
  const [res, setRes] = useState<RobustResp | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const run = async () => {
    setBusy(true)
    setError('')
    try {
      setRes(await api.robust(params, { from, split }))
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const base = res?.base
  const rowsByGroup: [string, string, RobustResp['rows']][] = []
  for (const r of res?.rows ?? []) {
    const last = rowsByGroup[rowsByGroup.length - 1]
    if (last && last[0] === r.group) last[2].push(r)
    else rowsByGroup.push([r.group, r.label, [r]])
  }
  const range = (rows: RobustResp['rows']) => {
    const xs = [strat(base, 'layered')?.totalRet, ...rows.map((r) => strat(r.outcome, 'layered')?.totalRet)].filter((x): x is number => x !== undefined)
    return xs.length ? `${pct(Math.min(...xs))} ~ ${pct(Math.max(...xs))}` : '—'
  }
  const years = res?.years ?? []
  const beat = years.filter((y) => (strat(y.outcome, 'layered')?.totalRet ?? 0) > (strat(y.outcome, 'valuation')?.totalRet ?? 0)).length

  const cells = (o: RobustOutcome | undefined) => {
    const t = strat(o, 'thermometer'), l = strat(o, 'layered'), v = strat(o, 'valuation')
    return (
      <>
        <td className="num mono">{o ? `${o.buySignals} / ${o.sellSignals}` : '—'}</td>
        <td className={`num mono ${dir(t?.totalRet)}`}>{t ? pct(t.totalRet) : '—'}</td>
        <td className="num mono muted">{t ? pct(t.maxDD) : '—'}</td>
        <td className={`num mono ${dir(l?.totalRet)}`}>{l ? pct(l.totalRet) : '—'}</td>
        <td className="num mono muted">{l ? pct(l.maxDD) : '—'}</td>
        <td className="num mono">{v ? pct(v.totalRet) : '—'}</td>
      </>
    )
  }

  return (
    <section className="panel">
      <div className="panel-head">
        <strong>稳健性检验</strong>
        <span className="muted">用左侧当前参数：每次只把一组参数改动 ±20%，看结论是否大起大落；再按自然年分段。不排序、不挑参数。</span>
        <button className="btn" style={{ marginLeft: 'auto' }} disabled={busy || !params} onClick={run}>
          {busy ? '计算中…' : res ? '重新计算' : '运行稳健性检验'}
        </button>
      </div>
      {error && <div className="error" role="alert" style={{ margin: '0 16px 12px' }}>{error}</div>}
      {res && base && (
        <>
          <p className="disclaimer robust-note">
            {res.from} 起，参数 {res.paramsVersion}，耗时 {res.elapsedMs} ms。顶部武装线、冷却顶线、昂贵线这类上沿阈值按“距 100 的距离”扰动；
            整数参数取整后不变时改为 ±1。信号合计少于 5 个的行标“样本少”。估值档基线随估值界线变化，因此逐行列出。
          </p>
          <div className="scroll-x">
            <table className="table static robust">
              <thead>
                <tr>
                  <th>参数</th><th>取值</th>
                  <th className="num">底 / 顶信号</th>
                  <th className="num">温度计收益</th><th className="num">回撤</th>
                  <th className="num">加减仓收益</th><th className="num">回撤</th>
                  <th className="num">估值档收益</th>
                  <th className="num">加减仓收益区间</th>
                </tr>
              </thead>
              <tbody>
                <tr className="emph">
                  <td>基准</td><td className="muted">当前参数</td>
                  {cells(base)}
                  <td />
                </tr>
                {rowsByGroup.map(([key, label, rows]) => (
                  <Fragment key={key}>
                    {rows.map((r, i) => (
                      <tr key={r.level} className={i === 0 ? 'group-start' : ''}>
                        {i === 0 && <td rowSpan={rows.length}>{label}</td>}
                        <td className="mono small">
                          {r.level < 0 ? '−' : '+'}20%：{r.values}
                          {r.skip && <div className="muted">{r.skip}</div>}
                          {r.outcome?.few && <span className="tag tag-warn small-tag">样本少</span>}
                        </td>
                        {r.outcome ? cells(r.outcome) : <td colSpan={6} className="muted">跳过</td>}
                        {i === 0 && <td rowSpan={rows.length} className="num mono">{range(rows)}</td>}
                      </tr>
                    ))}
                  </Fragment>
                ))}
              </tbody>
            </table>
          </div>

          <div className="panel-head">
            <strong>分年度表现</strong>
            <span className="muted">参数固定，未在各年内重新校准；持仓状态由之前的信号延续。加减仓跑赢估值档 {beat} / {years.length} 年。</span>
          </div>
          <div className="scroll-x">
            <table className="table static robust">
              <thead>
                <tr>
                  <th>年份</th><th className="num">底 / 顶信号</th>
                  <th className="num">温度计</th><th className="num">加减仓</th><th className="num">估值档</th><th className="num">买入持有</th>
                  <th className="num">加减仓 − 估值档</th><th className="num">加减仓回撤</th>
                </tr>
              </thead>
              <tbody>
                {years.map((y) => {
                  const t = strat(y.outcome, 'thermometer'), l = strat(y.outcome, 'layered'), v = strat(y.outcome, 'valuation'), h = strat(y.outcome, 'hold')
                  const diff = l && v ? l.totalRet - v.totalRet : undefined
                  return (
                    <tr key={y.from} className={y.from >= split ? 'oos' : ''}>
                      <td>{y.label}{y.from >= split && <span className="muted small"> · 样本外</span>}</td>
                      <td className="num mono">{y.outcome.buySignals} / {y.outcome.sellSignals}</td>
                      <td className={`num mono ${dir(t?.totalRet)}`}>{t ? pct(t.totalRet) : '—'}</td>
                      <td className={`num mono ${dir(l?.totalRet)}`}>{l ? pct(l.totalRet) : '—'}</td>
                      <td className={`num mono ${dir(v?.totalRet)}`}>{v ? pct(v.totalRet) : '—'}</td>
                      <td className={`num mono ${dir(h?.totalRet)}`}>{h ? pct(h.totalRet) : '—'}</td>
                      <td className={`num mono ${dir(diff)}`}>{diff !== undefined ? pct(diff) : '—'}</td>
                      <td className="num mono muted">{l ? pct(l.maxDD) : '—'}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  )
}
