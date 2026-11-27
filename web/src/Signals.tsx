import { useEffect, useMemo, useState } from 'react'
import { api, type ScoreSeries, type SignalsResp } from './api'
import EChart from './EChart'
import { asMarkers, priceWithSignals } from './charts'
import { EVENT_TEXT, fmt, PHASE_TEXT, TIER_TEXT } from './temperature'
import { useTheme } from './theme'

const FILTERS = [
  { key: 'signal', label: '买卖信号', types: ['BUY_DIP', 'SELL_PEAK'] },
  { key: 'state', label: '含武装 / 失效', types: ['BUY_DIP', 'SELL_PEAK', 'ARMED_BOTTOM', 'ARMED_TOP', 'DISARMED'] },
  { key: 'tier', label: '估值档切换', types: ['VALUATION_TIER_CHANGE'] },
  { key: 'all', label: '全部', types: [] as string[] },
] as const

const TYPE_CLASS: Record<string, string> = {
  BUY_DIP: 'tag-cold', SELL_PEAK: 'tag-hot', ARMED_BOTTOM: 'tag-muted', ARMED_TOP: 'tag-muted',
  DISARMED: 'tag-muted', VALUATION_TIER_CHANGE: 'tag-muted', DATA_DEGRADED: 'tag-warn',
}

export default function Signals() {
  const [data, setData] = useState<SignalsResp | null>(null)
  const [prices, setPrices] = useState<ScoreSeries | null>(null)
  const [filter, setFilter] = useState<(typeof FILTERS)[number]['key']>('signal')
  const [error, setError] = useState('')
  const { theme } = useTheme()

  useEffect(() => {
    api.signals().then(setData, (e) => setError(e.message))
    api.scores().then(setPrices, (e) => setError(e.message))
  }, [])

  const shown = useMemo(() => {
    const f = FILTERS.find((x) => x.key === filter)!
    const evs = data?.events ?? []
    return (f.types.length ? evs.filter((e) => (f.types as readonly string[]).includes(e.type)) : evs).slice().reverse()
  }, [data, filter])

  const option = useMemo(() => {
    if (!prices || !data) return null
    // 从温度有值的日子开始画，避开预热期
    const i0 = Math.max(0, prices.series.tS.findIndex((x) => x !== null))
    return priceWithSignals(prices.dates.slice(i0), prices.series.close.slice(i0), asMarkers(data.events))
  }, [prices, data, theme])

  if (error) return <div className="error" role="alert">{error}</div>
  if (!data) return null
  const nSignals = data.events.filter((e) => e.type === 'BUY_DIP' || e.type === 'SELL_PEAK').length

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>信号</h1>
          <p className="sub">
            当前状态：<strong>{PHASE_TEXT[data.phase] ?? '—'}</strong>
            <span className="muted">（{data.phaseDate || '—'} · 参数 {data.paramsVersion} · 历史买卖信号 {nSignals} 个）</span>
          </p>
        </div>
      </div>

      {option && (
        <section className="panel">
          <div className="panel-head">
            <strong>信号位置</strong>
            <span className="muted">▲ 底部信号，▼ 顶部信号，空心为估值昂贵时的观察级底部信号</span>
          </div>
          <EChart option={option} height={360} />
        </section>
      )}

      <section className="panel">
        <div className="panel-head">
          <strong>事件</strong>
          <div className="seg" role="group" aria-label="事件筛选" style={{ marginLeft: 'auto' }}>
            {FILTERS.map((f) => (
              <button key={f.key} className={f.key === filter ? 'on' : ''} aria-pressed={f.key === filter} onClick={() => setFilter(f.key)}>
                {f.label}
              </button>
            ))}
          </div>
        </div>
        {shown.length === 0 ? (
          <div className="empty">没有符合条件的事件。</div>
        ) : (
          <table className="table static">
            <thead>
              <tr>
                <th>日期</th>
                <th>事件</th>
                <th className="num">收盘</th>
                <th className="num">T_s</th>
                <th>估值</th>
                <th>说明</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((e, i) => (
                <tr key={`${e.date}-${e.type}-${i}`}>
                  <td className="mono">{e.date}</td>
                  <td>
                    <span className={`tag ${TYPE_CLASS[e.type] ?? 'tag-muted'}`}>{EVENT_TEXT[e.type] ?? e.type}</span>
                    {e.observe && <span className="tag tag-muted" style={{ marginLeft: 4 }}>观察</span>}
                  </td>
                  <td className="num mono">{fmt(e.close, 2)}</td>
                  <td className="num mono">{fmt(e.tS)}</td>
                  <td>{e.tier ? `${TIER_TEXT[e.tier]} ${fmt(e.v, 0)}` : '—'}</td>
                  <td className="muted">{e.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  )
}
