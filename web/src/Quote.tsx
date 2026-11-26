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

/** 最新值在自身历史（非空、正数）中的分位，0–100。 */
function percentile(xs: (number | null)[]): { last: number; pct: number; n: number; from: number } | null {
  const idx = xs.map((x, i) => (x !== null && x > 0 ? i : -1)).filter((i) => i >= 0)
  if (idx.length < 20) return null
  const vals = idx.map((i) => xs[i] as number)
  const last = vals[vals.length - 1]
  const below = vals.filter((v) => v < last).length
  const equal = vals.filter((v) => v === last).length
  return { last, pct: ((below + 0.5 * equal) / vals.length) * 100, n: vals.length, from: idx[0] }
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

  const prices = useMemo(() => (data ? (adjusted ? data.adjusted : data.close) : []), [data, adjusted])
  const hasK = !!data?.high

  const option = useMemo<EChartsOption | null>(() => {
    if (!data || data.dates.length === 0) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line')
    const colors = [cssVar('--t-hot-1'), cssVar('--t-cold-2'), cssVar('--dim-2')]
    const up = '#d8453b', down = '#1f8a4c'
    const mas = data.kind === 'ofund' ? [] : MAS.map((n, i) => ({ name: `MA${n}`, type: 'line', data: sma(prices, n), showSymbol: false,
      lineStyle: { width: 1, color: colors[i] }, itemStyle: { color: colors[i] } }))
    const zoom = { start: 85, end: 100 }
    if (hasK) {
      // K 线：后复权模式下按当日复权比例（后复权价 ÷ 不复权收盘）缩放 OHLC
      const k = data.dates.map((_, i) => {
        const o = data.open![i], h = data.high![i], l = data.low![i]
        if (o === null || h === null || l === null) return ['-', '-', '-', '-']
        const f = adjusted ? data.adjusted[i] / data.close[i] : 1
        return [o * f, prices[i], l * f, h * f]
      })
      const vols = data.dates.map((_, i) => {
        const v = data.volume![i]
        if (v === null) return null
        const o = data.open![i] ?? prices[i]
        return { value: v / 100, itemStyle: { color: data.close[i] >= (o ?? 0) ? up : down } }
      })
      return {
        animation: false,
        tooltip: { trigger: 'axis', axisPointer: { type: 'cross' } },
        legend: { top: 0, left: 0, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
        axisPointer: { link: [{ xAxisIndex: 'all' }] },
        grid: [{ left: 64, right: 24, top: 32, height: '62%' }, { left: 64, right: 24, top: '76%', bottom: 48 }],
        xAxis: [
          { type: 'category', data: data.dates, axisLine: { lineStyle: { color: line } }, axisLabel: { show: false } },
          { type: 'category', gridIndex: 1, data: data.dates, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
        ],
        yAxis: [
          { type: 'value', scale: true, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
          { type: 'value', gridIndex: 1, splitLine: { show: false }, axisLabel: { color: ink, formatter: (v: number) => (v >= 1e4 ? `${(v / 1e4).toFixed(0)}万` : `${v}`) } },
        ],
        dataZoom: [{ type: 'inside', xAxisIndex: [0, 1], ...zoom }, { type: 'slider', xAxisIndex: [0, 1], height: 16, bottom: 8, borderColor: line, textStyle: { color: ink }, ...zoom }],
        series: [
          { name: adjusted ? '后复权 K 线' : 'K 线', type: 'candlestick', data: k,
            itemStyle: { color: up, color0: down, borderColor: up, borderColor0: down } },
          ...mas,
          { name: '成交量（手）', type: 'bar', xAxisIndex: 1, yAxisIndex: 1, data: vols },
        ],
      }
    }
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
        ...mas,
      ],
    }
  }, [data, prices, adjusted, hasK])

  const valuation = useMemo(() => {
    if (!data?.peTtm || !data.pb) return null
    const pe = percentile(data.peTtm), pb = percentile(data.pb)
    if (!pe && !pb) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line')
    const from = Math.min(pe?.from ?? Infinity, pb?.from ?? Infinity)
    const dates = data.dates.slice(from)
    const option: EChartsOption = {
      animation: false,
      tooltip: { trigger: 'axis', valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(2) : '—') },
      legend: { top: 0, left: 0, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
      grid: { left: 56, right: 56, top: 32, bottom: 48 },
      xAxis: { type: 'category', data: dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      yAxis: [
        { type: 'value', scale: true, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
        { type: 'value', scale: true, splitLine: { show: false }, axisLabel: { color: ink } },
      ],
      dataZoom: [{ type: 'inside' }, { type: 'slider', height: 16, bottom: 8, borderColor: line, textStyle: { color: ink } }],
      series: [
        // 亏损时 PE 为负，没有估值意义，画成空缺
        { name: 'PE(TTM)（左轴）', type: 'line', data: data.peTtm.slice(from).map((x) => (x !== null && x > 0 ? x : null)), showSymbol: false, connectNulls: false,
          lineStyle: { width: 1.4, color: cssVar('--accent') }, itemStyle: { color: cssVar('--accent') } },
        { name: 'PB（右轴）', type: 'line', yAxisIndex: 1, data: data.pb.slice(from), showSymbol: false,
          lineStyle: { width: 1.2, color: cssVar('--t-hot-1') }, itemStyle: { color: cssVar('--t-hot-1') } },
      ],
    }
    return { pe, pb, option, from: data.dates[from] }
  }, [data])

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
          <p className="sub">
            本地日线。股票有扩展数据（stockdb 回填，2015 年起）时显示 K 线、成交量与 PE / PB；场内基金与场外基金只有收盘价或净值。
          </p>
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
          {option ? <EChart option={option} height={hasK ? 520 : 460} /> : <div className="empty">本地没有这只证券的数据。</div>}
          {data.kind === 'stock' && !hasK && n > 0 && <p className="disclaimer" style={{ padding: '0 16px 12px' }}>没有扩展数据（K 线、成交量、估值）：在数据页回填扩展字段，或运行 mt ext-backfill。</p>}
        </section>
      )}
      {valuation && (
        <section className="panel chart-panel">
          <div className="panel-head">
            <strong>估值</strong>
            <span className="muted">
              {valuation.pe && <>PE(TTM) {valuation.pe.last.toFixed(2)}，处于 {valuation.from} 以来 {valuation.pe.pct.toFixed(0)}% 分位</>}
              {valuation.pe && valuation.pb && '；'}
              {valuation.pb && <>PB {valuation.pb.last.toFixed(2)}，{valuation.pb.pct.toFixed(0)}% 分位</>}
              。来源 stockdb，口径未与其他来源核实；亏损期 PE 不计入。
            </span>
          </div>
          <EChart option={valuation.option} height={320} />
        </section>
      )}
      {!data && !error && <div className="panel empty">搜索一只证券查看走势。</div>}
    </div>
  )
}
