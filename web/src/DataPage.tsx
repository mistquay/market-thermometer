import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, ApiError, type JobStatus, type Metric, type Series } from './api'
import LineChart from './LineChart'

const STALE_DAYS = 7

function formatter(unit: string): (v: number) => string {
  if (unit === '元') return (v) => `${(v / 1e8).toLocaleString('zh-CN', { maximumFractionDigits: 0 })} 亿`
  if (unit === '%') return (v) => `${v.toFixed(2)}%`
  return (v) => v.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}

function daysSince(date: string) {
  return Math.floor((Date.now() - new Date(date + 'T00:00:00+08:00').getTime()) / 86400000)
}

// 按路由优先级取第一个有数据的来源的覆盖情况
function primaryCoverage(m: Metric) {
  const cov = m.coverage ?? []
  for (const s of m.route ?? []) {
    const c = cov.find((x) => x.source === s)
    if (c) return c
  }
  return cov[0]
}

function Health({ m }: { m: Metric }) {
  const c = primaryCoverage(m)
  if (!c && (m.route ?? []).length === 0) return <span className="tag tag-muted">未配置来源</span>
  if (!c) return <span className="tag tag-bad">无数据</span>
  const d = daysSince(c.last)
  if (d > STALE_DAYS) return <span className="tag tag-warn">{d} 天未更新</span>
  return <span className="tag tag-ok">正常</span>
}

const JOB_TEXT: Record<string, string> = {
  sync: '增量同步',
  backfill: '全量回填',
  breadth: '市场广度更新',
  'breadth-backfill': '回填市场广度',
  funds: '基金净值同步',
  crosscheck: '交叉核对',
  gapfill: '补缺口',
  names: '证券名称',
  'ext-backfill': '回填扩展字段',
  derived: '派生指标重算',
}

const STATE_TEXT: Record<JobStatus['state'], string> = {
  running: '运行中',
  done: '完成',
  partial: '部分失败',
  failed: '失败',
}

export default function DataPage() {
  const [metrics, setMetrics] = useState<Metric[]>([])
  const [selected, setSelected] = useState<string>('')
  const [series, setSeries] = useState<Series | null>(null)
  const [job, setJob] = useState<JobStatus | null>(null)
  const [error, setError] = useState('')
  const [stocks, setStocks] = useState<Awaited<ReturnType<typeof api.stockStats>> | null>(null)
  const loadStocks = useCallback(() => api.stockStats().then(setStocks, () => {}), [])

  const loadMetrics = useCallback(() => {
    api.metrics().then((ms) => {
      setMetrics(ms)
      setSelected((cur) => cur || ms.find((m) => primaryCoverage(m))?.id || ms[0]?.id || '')
    }, (e) => setError(e.message))
  }, [])

  useEffect(() => {
    loadMetrics()
    loadStocks()
    api.currentJob().then(setJob, () => {})
  }, [loadMetrics])

  useEffect(() => {
    if (!selected) return
    let stale = false
    api.series(selected).then((s) => !stale && setSeries(s), (e) => setError(e.message))
    return () => {
      stale = true
    }
  }, [selected])

  // 任务运行中时轮询进度，结束后刷新覆盖情况
  const running = job?.state === 'running'
  useEffect(() => {
    if (!running) return
    const t = setInterval(() => {
      api.currentJob().then((j) => {
        setJob(j)
        if (j?.state !== 'running') {
          loadMetrics()
          loadStocks()
          setSelected((cur) => {
            if (cur) api.series(cur).then(setSeries, () => {})
            return cur
          })
        }
      }, () => {})
    }, 1500)
    return () => clearInterval(t)
  }, [running, loadMetrics, loadStocks])

  async function startBreadth() {
    if (!confirm('从本地 stockdb 逐只读取全部 A 股（含退市）日线，重新计算市场广度。需要 stockdb.exe 已启动，耗时可能较长（几十分钟），期间可以离开本页。确定开始？')) return
    setError('')
    try {
      setJob(await api.startBreadth(true))
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }

  async function startExt() {
    if (!confirm('从本地 stockdb 逐只读取 2015 年以来 A 股的开高低收、成交量、PE / PB、市值，供行情页 K 线与估值使用。需要 stockdb.exe 已启动；数据库约增加几百 MB；已回填的会跳过。确定开始？')) return
    setError('')
    try {
      setJob(await api.startExtBackfill())
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }

  async function startSync(full: boolean) {
    if (full && !confirm('全量回填会从配置的起始日期重新拉取所有指标，确定继续？')) return
    setError('')
    try {
      setJob(await api.startSync(full))
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }

  const current = metrics.find((m) => m.id === selected)
  const format = useMemo(() => formatter(current?.unit ?? ''), [current?.unit])
  const healthy = metrics.filter((m) => {
    const c = primaryCoverage(m)
    return c && daysSince(c.last) <= STALE_DAYS
  }).length

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>数据</h1>
          <p className="sub">
            {metrics.length} 个指标，{healthy} 个数据正常。数据来自可插拔的数据源插件，按路由顺序取数，主源失败时自动回退。
          </p>
        </div>
        <div className="actions">
          <button className="btn" disabled={running} onClick={startBreadth}>回填市场广度</button>
          <button className="btn" disabled={running} onClick={startExt}>回填 K 线与估值</button>
          <button className="btn" disabled={running} onClick={() => startSync(true)}>全量回填</button>
          <button className="btn btn-primary" disabled={running} onClick={() => startSync(false)}>
            {running ? '同步中…' : '增量同步'}
          </button>
        </div>
      </div>

      {error && <div className="error" role="alert">{error}</div>}

      {job && (
        <section className="panel job">
          <div className="job-head">
            <strong>{JOB_TEXT[job.kind] ?? job.kind} #{job.id}</strong>
            <span className={`tag tag-${job.state === 'done' ? 'ok' : job.state === 'running' ? 'muted' : job.state === 'partial' ? 'warn' : 'bad'}`}>
              {STATE_TEXT[job.state]}
            </span>
            <span className="muted">
              {job.done}/{job.total}
              {job.current && ` · 正在拉取 ${job.current}`}
            </span>
          </div>
          <div className="progress" role="progressbar" aria-valuemin={0} aria-valuemax={job.total} aria-valuenow={job.done}>
            <div style={{ width: `${job.total ? (job.done / job.total) * 100 : 0}%` }} />
          </div>
          {job.log && job.log.length > 0 && (
            <pre className="log">{job.log.join('\n')}</pre>
          )}
        </section>
      )}

      {stocks && (
        <section className="panel stock-panel">
          <div>
            <strong>本地个股 / 场内基金日线</strong>
            <span className="muted">（我们自己的数据，市场广度只从这里计算；数据源：{stocks.source || '未配置'}）</span>
          </div>
          {stocks.stats.withBars === 0 ? (
            <span className="muted">尚未回填。点击“回填市场广度”从数据源拉取全部 A 股日线（含退市股）。</span>
          ) : (
            <span className="mono">
              {stocks.stats.withBars.toLocaleString('zh-CN')} 只股票（其中退市 {stocks.stats.delisted.toLocaleString('zh-CN')}）· {stocks.stats.funds.toLocaleString('zh-CN')} 只场内基金 · {stocks.stats.first} ~ {stocks.stats.last}
            </span>
          )}
        </section>
      )}

      <section className="panel">
        <table className="table">
          <thead>
            <tr>
              <th>指标</th>
              <th>数据源路由</th>
              <th>覆盖区间</th>
              <th className="num">条数</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            {metrics.map((m) => {
              const c = primaryCoverage(m)
              const have = new Set((m.coverage ?? []).map((x) => x.source))
              return (
                <tr
                  key={m.id}
                  className={m.id === selected ? 'selected' : ''}
                  onClick={() => setSelected(m.id)}
                  tabIndex={0}
                  onKeyDown={(e) => e.key === 'Enter' && setSelected(m.id)}
                >
                  <td>
                    <div className="metric-name">{m.name}</div>
                    <code className="metric-id">{m.id}</code>
                    {m.note && <div className="metric-note">{m.note}</div>}
                  </td>
                  <td>
                    {(m.route ?? []).length === 0 && (m.coverage ?? []).length > 0 && <span className="chip chip-on" title="由本地个股日线派生">本地派生</span>}
                    {(m.route ?? []).map((s, i) => (
                      <span key={s} className={`chip ${have.has(s) ? 'chip-on' : ''}`} title={i === 0 ? '主源' : '备用源'}>
                        {s}
                      </span>
                    ))}
                  </td>
                  <td className="mono">{c ? `${c.first} ~ ${c.last}` : '—'}</td>
                  <td className="num mono">{c ? c.count.toLocaleString('zh-CN') : '—'}</td>
                  <td><Health m={m} /></td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </section>

      {current && (
        <section className="panel">
          <div className="chart-head">
            <strong>{current.name}</strong>
            <span className="muted">
              {series?.source ? `来源 ${series.source} · ${series.points.length.toLocaleString('zh-CN')} 条` : '暂无数据'}
            </span>
          </div>
          {series && series.points.length > 0 ? (
            <LineChart points={series.points} name={current.name} format={format} />
          ) : (
            <div className="empty">该指标还没有数据，先执行一次同步。</div>
          )}
        </section>
      )}
    </div>
  )
}
