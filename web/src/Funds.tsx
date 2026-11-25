import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, ApiError, type FundInfo, type FundNAV, type JobStatus, type WatchedFund } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'

export default function Funds() {
  const [watched, setWatched] = useState<WatchedFund[]>([])
  const [infoCount, setInfoCount] = useState(0)
  const [configured, setConfigured] = useState(true)
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<FundInfo[]>([])
  const [selected, setSelected] = useState('')
  const [navs, setNavs] = useState<FundNAV[]>([])
  const [job, setJob] = useState<JobStatus | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api.funds().then((r) => {
      setWatched(r.watched)
      setInfoCount(r.infoCount)
      setConfigured(r.configured)
      setSelected((cur) => cur || r.watched.find((f) => f.count > 0)?.code || '')
    }, (e) => setError(e.message))
  }, [])

  useEffect(load, [load])

  // 搜索：输入停顿 250ms 后查询本地基金清单
  useEffect(() => {
    if (!q.trim()) {
      setHits([])
      return
    }
    const t = setTimeout(() => api.searchFunds(q).then(setHits, () => {}), 250)
    return () => clearTimeout(t)
  }, [q])

  useEffect(() => {
    if (!selected) return
    api.fundNAV(selected).then(setNavs, (e) => setError(e.message))
  }, [selected])

  const running = job?.state === 'running'
  useEffect(() => {
    if (!running) return
    const t = setInterval(() => {
      api.currentJob().then((j) => {
        setJob(j)
        if (j?.state !== 'running') {
          load()
          if (selected) api.fundNAV(selected).then(setNavs, () => {})
        }
      }, () => {})
    }, 1500)
    return () => clearInterval(t)
  }, [running, load, selected])

  async function run(f: () => Promise<unknown>) {
    setError('')
    try {
      await f()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }

  const add = (code: string) =>
    run(async () => {
      await api.watchFund(code)
      setQ('')
      setHits([])
      load()
    })

  const sync = () => run(async () => setJob(await api.startFunds()))

  const current = watched.find((f) => f.code === selected)
  const option = useMemo<EChartsOption | null>(() => {
    if (navs.length === 0) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line')
    return {
      animation: false,
      grid: { left: 56, right: 24, top: 32, bottom: 56 },
      tooltip: { trigger: 'axis' },
      legend: { top: 0, right: 0, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
      xAxis: { type: 'category', data: navs.map((n) => n.date), boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      yAxis: { type: 'value', scale: true, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      dataZoom: [{ type: 'inside' }, { type: 'slider', height: 18, bottom: 8, borderColor: line, textStyle: { color: ink } }],
      series: [
        { name: '累计净值', type: 'line', showSymbol: false, data: navs.map((n) => n.accNav), lineStyle: { width: 1.6, color: cssVar('--accent') }, itemStyle: { color: cssVar('--accent') } },
        { name: '单位净值', type: 'line', showSymbol: false, data: navs.map((n) => n.nav), lineStyle: { width: 1.2, color: ink }, itemStyle: { color: ink } },
      ],
    }
  }, [navs])

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>基金</h1>
          <p className="sub">
            场外基金净值只同步自选基金，每日同步时自动更新。场内基金（ETF / LOF）的日线在“数据”页随个股一起同步。
            {infoCount > 0 && <span className="muted"> 本地基金清单 {infoCount.toLocaleString('zh-CN')} 只。</span>}
          </p>
        </div>
        <div className="actions">
          <button className="btn btn-primary" disabled={running || !configured} onClick={sync}>
            {running ? '同步中…' : infoCount === 0 ? '下载基金清单并同步' : '同步净值'}
          </button>
        </div>
      </div>

      {!configured && <div className="notice">未配置场外基金数据源（config.yaml 的 funds.sources）。</div>}
      {error && <div className="error" role="alert">{error}</div>}
      {job && job.kind === 'funds' && job.log && job.log.length > 0 && <pre className="log panel">{job.log.join('\n')}</pre>}

      <section className="panel fund-search">
        <input
          placeholder={infoCount > 0 ? '输入基金代码或名称搜索，如 110022、消费' : '先点击右上角下载基金清单，或直接输入 6 位代码添加'}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && /^\d{6}$/.test(q.trim()) && add(q.trim())}
        />
        {hits.length > 0 && (
          <ul className="fund-hits">
            {hits.map((f) => (
              <li key={f.code}>
                <span className="mono">{f.code}</span> {f.name} <span className="muted">{f.type}</span>
                <button className="btn btn-ghost" onClick={() => add(f.code)} disabled={watched.some((w) => w.code === f.code)}>
                  {watched.some((w) => w.code === f.code) ? '已添加' : '添加'}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="panel">
        {watched.length === 0 ? (
          <div className="empty">还没有自选基金。</div>
        ) : (
          <table className="table">
            <thead>
              <tr><th>基金</th><th>类型</th><th className="num">单位净值</th><th className="num">累计净值</th><th>净值区间</th><th className="num">条数</th><th></th></tr>
            </thead>
            <tbody>
              {watched.map((f) => (
                <tr key={f.code} className={f.code === selected ? 'selected' : ''} onClick={() => setSelected(f.code)}>
                  <td><div className="metric-name">{f.name || '（名称未知）'}</div><code className="metric-id">{f.code}</code></td>
                  <td className="muted">{f.type || '—'}</td>
                  <td className="num mono">{f.count ? f.nav.toFixed(4) : '—'}</td>
                  <td className="num mono">{f.count ? f.accNav.toFixed(4) : '—'}</td>
                  <td className="mono">{f.count ? `${f.first} ~ ${f.last}` : '未同步'}</td>
                  <td className="num mono">{f.count.toLocaleString('zh-CN')}</td>
                  <td className="num">
                    <button className="btn btn-ghost" onClick={(e) => { e.stopPropagation(); run(async () => { await api.unwatchFund(f.code); if (selected === f.code) { setSelected(''); setNavs([]) } load() }) }}>
                      移除
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {current && (
        <section className="panel">
          <div className="panel-head">
            <strong>{current.name || current.code}</strong>
            <span className="muted">累计净值含分红，适合比较长期收益</span>
          </div>
          {option ? <EChart option={option} height={360} /> : <div className="empty">尚无净值，点击“同步净值”。</div>}
        </section>
      )}
    </div>
  )
}
