import { useEffect, useMemo, useState } from 'react'
import { api, type SecurityHit, type SecuritySeries } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { pct } from './temperature'

const KIND_TEXT: Record<string, string> = { stock: '股票', fund: '场内基金', ofund: '场外基金' }
const MAS = [20, 60, 250]

function sma(xs: number[], n: number): (number | null)[] {
  const out: (number | null)[] = []
  let s = 0
  xs.forEach((x, i) => {
    s += x
    if (i >= n) s -= xs[i - n]
    out.push(i >= n - 1 ? s / n : null)
  })
  return out
}

/** 当前地址中的证券：#/quote/{kind}/{code} */
function fromHash(): { kind: string; code: string } | null {
  const m = location.hash.match(/^#\/quote\/(stock|fund|ofund)\/(\d{6})/)
  return m ? { kind: m[1], code: m[2] } : null
}

export default function Quote() {
  const [target, setTarget] = useState(fromHash)
  const [data, setData] = useState<SecuritySeries | null>(null)
  const [adjusted, setAdjusted] = useState(true)
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<SecurityHit[]>([])
  const [error, setError] = useState('')

  useEffect(() => {
    const on = () => setTarget(fromHash())
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  useEffect(() => {
    if (!target) return
    setError('')
    api.securitySeries(target.kind, target.code).then(setData, (e) => { setData(null); setError(e.message) })
  }, [target])
  useEffect(() => {
    if (!q.trim()) { setHits([]); return }
    const t = setTimeout(() => api.searchSecurities(q).then((r) => setHits(r.items), () => {}), 250)
    return () => clearTimeout(t)
  }, [q])

  const prices = data ? (adjusted ? data.adjusted : data.close) : []
  const option = useMemo<EChartsOption | null>(() => {
    if (!data || data.dates.length === 0) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line')
    const colors = [cssVar('--t-hot-1'), cssVar('--t-cold-2'), cssVar('--dim-2')]
    return {
      animation: false,
      grid: { left: 64, right: 24, top: 36, bottom: 56 },
      tooltip: { trigger: 'axis', valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(3) : '—') },
      legend: { top: 0, left: 0, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
      xAxis: { type: 'category', data: data.dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      yAxis: { type: 'value', scale: true, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      dataZoom: [{ type: 'inside', start: 70 }, { type: 'slider', height: 18, bottom: 8, start: 70, borderColor: line, textStyle: { color: ink } }],
      series: [
        { name: data.kind === 'ofund' ? (adjusted ? '累计净值' : '单位净值') : adjusted ? '后复权收盘' : '收盘', type: 'line', data: prices, showSymbol: false,
          lineStyle: { width: 1.6, color: cssVar('--accent') }, itemStyle: { color: cssVar('--accent') } },
        ...(data.kind === 'ofund' ? [] : MAS.map((n, i) => ({ name: `MA${n}`, type: 'line', data: sma(prices, n), showSymbol: false,
          lineStyle: { width: 1, color: colors[i] }, itemStyle: { color: colors[i] } }))),
      ],
    }
  }, [data, prices, adjusted])

  const n = prices.length
  const stats = n > 1 ? {
    last: prices[n - 1],
    d1: prices[n - 1] / prices[n - 2] - 1,
    y1: n > 250 ? prices[n - 1] / prices[n - 251] - 1 : null,
    hi: Math.max(...prices.slice(-250)),
    lo: Math.min(...prices.slice(-250)),
  } : null

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>行情</h1>
          <p className="sub">本地日线（股票、场内基金：不复权收盘价与后复权价；场外基金：单位 / 累计净值）。本地只存收盘价，暂无 K 线与成交量。</p>
        </div>
      </div>
      <section className="panel fund-search">
        <input placeholder="输入代码或名称搜索" value={q} onChange={(e) => setQ(e.target.value)} />
        {hits.length > 0 && (
          <ul className="fund-hits">
            {hits.map((h) => (
              <li key={h.kind + h.code}>
                <span className="mono">{h.code}</span> {h.name || '（无名称）'} <span className="tag tag-muted">{KIND_TEXT[h.kind]}</span>
                {!h.hasData && <span className="tag tag-warn">本地无数据</span>}
                <a className="btn btn-ghost" href={`#/quote/${h.kind}/${h.code}`} onClick={() => { setQ(''); setHits([]) }}>查看</a>
              </li>
            ))}
          </ul>
        )}
      </section>
      {error && <div className="error" role="alert">{error}</div>}
      {data && (
        <section className="panel chart-panel">
          <div className="panel-head">
            <strong><span className="mono">{data.code}</span> {data.name}</strong>
            <span className="tag tag-muted">{KIND_TEXT[data.kind]}</span>
            {stats && (
              <span className="muted">
                最新 {stats.last.toFixed(3)}（{data.dates[n - 1]}）· 日涨跌 <span className={stats.d1 > 0 ? 'up' : stats.d1 < 0 ? 'down' : ''}>{pct(stats.d1, 2)}</span>
                {stats.y1 !== null && <> · 近一年 {pct(stats.y1)}</>} · 近一年区间 {stats.lo.toFixed(3)} ~ {stats.hi.toFixed(3)}
              </span>
            )}
            <span style={{ flex: 1 }} />
            <div className="seg">
              <button className={adjusted ? 'on' : ''} onClick={() => setAdjusted(true)}>{data.kind === 'ofund' ? '累计净值' : '后复权'}</button>
              <button className={adjusted ? '' : 'on'} onClick={() => setAdjusted(false)}>{data.kind === 'ofund' ? '单位净值' : '不复权'}</button>
            </div>
          </div>
          {option ? <EChart option={option} height={460} /> : <div className="empty">本地没有这只证券的数据。</div>}
        </section>
      )}
      {!data && !error && <div className="panel empty">搜索一只证券查看走势。</div>}
    </div>
  )
}
