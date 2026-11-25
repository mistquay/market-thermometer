import { useEffect, useMemo, useState } from 'react'
import { api, type DayScore, type Num, type Overview as OverviewData } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { EVENT_TEXT, fmt, PHASE_TEXT, TIER_COLOR, TIER_TEXT, zoneOf } from './temperature'

/** 0–100 的横条，带分区刻度和当前值标记。 */
function Scale({ value, marks, color }: { value: Num; marks: number[]; color: string }) {
  return (
    <div className="scale" aria-hidden>
      <div className="scale-track">
        {marks.map((m) => (
          <span key={m} className="scale-mark" style={{ left: `${m}%` }} />
        ))}
        {value !== null && (
          <span className="scale-dot" style={{ left: `${Math.min(100, Math.max(0, value))}%`, background: color }} />
        )}
      </div>
      <div className="scale-labels">
        <span>0</span>
        {marks.map((m) => (
          <span key={m} style={{ left: `${m}%` }}>{m}</span>
        ))}
        <span>100</span>
      </div>
    </div>
  )
}

function Delta({ cur, prev }: { cur: Num; prev: Num }) {
  if (cur === null || prev === null) return null
  const d = cur - prev
  if (Math.abs(d) < 0.05) return <span className="delta">较前日持平</span>
  return <span className="delta">较前日 {d > 0 ? '+' : ''}{d.toFixed(1)}</span>
}

type Dim = { name: string; score: Num; raw: string; enabled: boolean; hint: string }

function dims(d: DayScore, weights: number[]): Dim[] {
  return [
    {
      name: '资金热度', score: d.t[0], enabled: weights[0] > 0,
      raw: `换手率 ${fmt(d.heat, 2, '%')}`, hint: '中证全指成交额 ÷ 全A流通市值，5 日均值',
    },
    {
      name: '杠杆情绪', score: d.t[1], enabled: weights[1] > 0,
      raw: `融资买入占比 ${fmt(d.leverage, 1, '%')}`, hint: '融资买入额 ÷ 中证全指成交额，T-1，5 日均值',
    },
    {
      name: '市场广度', score: d.t[2], enabled: weights[2] > 0,
      raw: `站上 MA200 ${fmt(d.breadth, 1, '%')}`, hint: '全 A 股（含退市）后复权收盘价站上自身 200 日均线的占比',
    },
    {
      name: '价格动量', score: d.t[3], enabled: weights[3] > 0,
      raw: `日 RSI ${fmt(d.rsiD, 0)} / 周 RSI ${fmt(d.rsiW, 0)}`, hint: '0.4 × 日线 RSI(14) + 0.6 × 周线 RSI(14)',
    },
  ]
}

export default function Overview() {
  const [data, setData] = useState<OverviewData | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    api.overview().then(setData, (e) => setError(e.message))
  }, [])

  const chartOption = useMemo<EChartsOption | null>(() => {
    if (!data?.recent?.length) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line')
    return {
      animation: false,
      grid: { left: 44, right: 64, top: 28, bottom: 28 },
      tooltip: { trigger: 'axis' },
      legend: { top: 0, right: 0, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
      xAxis: {
        type: 'category', data: data.recent.map((r) => r.date),
        axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink },
      },
      yAxis: [
        { type: 'value', min: 0, max: 100, interval: 20, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
        { type: 'value', scale: true, splitLine: { show: false }, axisLabel: { color: ink } },
      ],
      series: [
        {
          name: '择时温度 T_s', type: 'line', showSymbol: false, data: data.recent.map((r) => r.tS),
          lineStyle: { width: 2, color: cssVar('--accent') }, itemStyle: { color: cssVar('--accent') },
          markArea: {
            silent: true,
            data: [
              [{ yAxis: 0, itemStyle: { color: cssVar('--t-cold-bg') } }, { yAxis: 20 }],
              [{ yAxis: 80, itemStyle: { color: cssVar('--t-hot-bg') } }, { yAxis: 100 }],
            ],
          },
        },
        {
          name: `${data.index} 收盘`, type: 'line', yAxisIndex: 1, showSymbol: false, data: data.recent.map((r) => r.close),
          lineStyle: { width: 1.2, color: ink }, itemStyle: { color: ink },
        },
      ],
    }
  }, [data])

  if (error) return <div className="error" role="alert">{error}</div>
  if (!data) return null
  const d = data.last
  if (!d) {
    return (
      <div className="page">
        <h1>总览</h1>
        <div className="panel empty">还没有打分结果。到"数据"页执行一次同步，同步结束后会自动打分。</div>
      </div>
    )
  }
  const p = data.params
  const zone = zoneOf(d.tS)
  const tierColor = TIER_COLOR[d.tier] ?? 'var(--ink-2)'

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>总览</h1>
          <p className="sub">
            {data.index} · {d.date} 收盘 {d.close?.toLocaleString('zh-CN', { minimumFractionDigits: 2 })}
            <span className="muted"> · 参数 {data.paramsVersion}</span>
          </p>
        </div>
      </div>

      {d.degraded && (
        <div className="notice" role="status">当日有一个维度缺数，温度按其余维度重新加权计算，本日不应据此触发信号。</div>
      )}

      <div className="hero">
        <section className="panel hero-card">
          <div className="hero-label">择时温度 T_s</div>
          <div className="hero-value">
            <span className="big" style={{ color: zone?.color }}>{fmt(d.tS)}</span>
            {zone && <span className="tag" style={{ color: zone.color, background: 'var(--bg)' }}>{zone.label}</span>}
            <Delta cur={d.tS} prev={data.prev?.tS ?? null} />
          </div>
          <Scale value={d.tS} marks={[20, 35, 70, 80]} color={zone?.color ?? 'var(--ink-2)'} />
          <p className="hero-foot">
            各维度得分加权后再取近 3 年分位、3 日平滑。≤20 为深度冰点，≥80 为过热。
            {d.nLow > 0 && ` 当前 ${d.nLow} 个维度处于冰点区。`}
            {d.nHigh > 0 && ` 当前 ${d.nHigh} 个维度处于过热区。`}
          </p>
        </section>

        <section className="panel hero-card">
          <div className="hero-label">估值背景 V</div>
          <div className="hero-value">
            <span className="big" style={{ color: tierColor }}>{fmt(d.v)}</span>
            {d.tier && <span className="tag" style={{ color: tierColor, background: 'var(--bg)' }}>{TIER_TEXT[d.tier]}</span>}
            <Delta cur={d.v} prev={data.prev?.v ?? null} />
          </div>
          <Scale value={d.v} marks={[p.cheapBelow, p.expensiveAbove]} color={tierColor} />
          <p className="hero-foot">
            股债利差 ERP {fmt(d.erp === null ? null : d.erp * 100, 2, '%')}（1/PE − 10 年国债收益率），
            在近 10 年中越高越便宜。V ≤ {p.cheapBelow} 为便宜，≥ {p.expensiveAbove} 为昂贵。
          </p>
        </section>
      </div>

      <section className="panel phase-panel">
        <div className="phase-main">
          <div className="hero-label">信号状态</div>
          <div className="phase-value">{PHASE_TEXT[data.phase ?? ''] ?? '—'}</div>
        </div>
        <div className="phase-events">
          {(data.recentEvents ?? []).length === 0 ? (
            <span className="muted">暂无事件</span>
          ) : (
            (data.recentEvents ?? []).map((e, i) => (
              <div key={i} className="phase-event">
                <span className="mono muted">{e.date}</span>
                <span className={`tag ${e.type === 'BUY_DIP' ? 'tag-cold' : e.type === 'SELL_PEAK' ? 'tag-hot' : 'tag-muted'}`}>
                  {EVENT_TEXT[e.type] ?? e.type}{e.observe ? '（观察）' : ''}
                </span>
                <span className="muted ellipsis">{e.reason}</span>
              </div>
            ))
          )}
          <a href="#/signals" className="muted small">全部事件 →</a>
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <strong>择时层各维度</strong>
          <span className="muted">0 = 历史最冷，100 = 历史最热（近 3 年分位）</span>
        </div>
        <table className="table dims">
          <tbody>
            {dims(d, p.weights).map((x) => {
              const z = zoneOf(x.score)
              return (
                <tr key={x.name} className={x.enabled ? '' : 'disabled'}>
                  <td>
                    <div className="metric-name">{x.name}</div>
                    <div className="metric-note">{x.hint}</div>
                  </td>
                  <td className="dim-bar-cell">
                    {x.enabled ? (
                      <div className="dim-bar">
                        <div style={{ width: `${x.score ?? 0}%`, background: z?.color ?? 'var(--line)' }} />
                      </div>
                    ) : (
                      <span className="tag tag-muted">未启用</span>
                    )}
                  </td>
                  <td className="num mono dim-score">{x.enabled ? fmt(x.score, 0) : ''}</td>
                  <td className="muted mono">{x.raw}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </section>

      {chartOption && (
        <section className="panel">
          <div className="panel-head">
            <strong>近一年</strong>
            <span className="muted">蓝底为冰点区（≤20），红底为过热区（≥80）</span>
          </div>
          <EChart option={chartOption} height={300} />
        </section>
      )}

      <p className="disclaimer">
        温度与估值档只描述市场相对历史的位置，阈值需在“回测”页校准，当前默认参数的回测表现并不理想，不构成投资建议。
      </p>
    </div>
  )
}
