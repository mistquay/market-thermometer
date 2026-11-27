import { useCallback, useEffect, useState } from 'react'
import { RefreshCw, ShieldCheck } from 'lucide-react'
import { api, ApiError, type JobStatus, type QualityReport } from './api'
import { useConfirm, useToast } from './ui/Feedback'

const DIFF_TEXT: Record<string, string> = {
  close: '收盘价不一致',
  mult: '除权乘数不一致',
  only_source: '本地缺、来源有',
  only_local: '本地有、来源缺',
}

export default function Quality() {
  const [rep, setRep] = useState<QualityReport | null>(null)
  const [job, setJob] = useState<JobStatus | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [showAllCov, setShowAllCov] = useState(false)
  const [gapQ, setGapQ] = useState('')
  const confirm = useConfirm()
  const toast = useToast()

  const load = useCallback(() => {
    setLoading(true)
    api.quality().then(setRep, (e) => setError(e.message)).finally(() => setLoading(false))
  }, [])
  useEffect(load, [load])

  const running = job?.state === 'running'
  useEffect(() => {
    if (!running) return
    const t = setInterval(() => {
      api.currentJob().then((j) => {
        setJob(j)
        if (j?.state !== 'running') load()
      }, () => {})
    }, 1500)
    return () => clearInterval(t)
  }, [running, load])

  const start = (f: () => Promise<JobStatus>) =>
    (async () => {
      setError('')
      try {
        setJob(await f())
      } catch (e) {
        setError(e instanceof ApiError ? e.message : String(e))
      }
    })()

  const check = (codes: string[] = []) => start(() => api.startCrossCheck(codes))
  const fill = async (source: string, codes: string[]) => {
    const ok = await confirm({
      title: '补齐缺失交易日',
      message: `用 ${source} 补齐 ${codes.length} 只股票在本地缺失的交易日？已有数据不会被覆盖，完成后重算市场广度。`,
      confirmText: '开始补齐',
    })
    if (ok) {
      toast.info(`已启动 ${source} 补缺口任务（${codes.length} 只）`)
      start(() => api.startGapFill(source, codes))
    }
  }
  const missingCodes = (source: string) => [...new Set((cc?.diffs ?? []).filter((d) => d.src === source && d.kind === 'only_source').map((d) => d.code))]

  const freshness = rep?.freshness ?? []
  const cov = rep?.coverage ?? []
  const lowDays = cov.filter((c) => c.low)
  const covShown = showAllCov ? [...cov].reverse() : lowDays
  const cc = rep?.crossCheck
  const allGaps = rep?.gaps ?? []
  const gaps = allGaps.filter((g) => g.code.includes(gapQ.trim()))

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>数据质量</h1>
          <p className="sub">
            只读本地数据检查新鲜度与缺口；交叉核对会请求数据源，手动触发。这里只检测、不修补。
            {rep && <span className="muted"> 交易日历最新 {rep.calendarLast}，计算耗时 {rep.elapsedMs} ms。</span>}
          </p>
        </div>
        <div className="actions">
          <button className="btn" disabled={loading} onClick={load}>
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
            {loading ? '计算中…' : '刷新'}
          </button>
          <button className="btn btn-primary" disabled={running} onClick={() => check()}>
            <ShieldCheck className="h-3.5 w-3.5" />
            {running ? '核对中…' : '抽样交叉核对'}
          </button>
        </div>
      </div>
      {error && <div className="error" role="alert">{error}</div>}
      {job && job.kind === 'crosscheck' && job.log && job.log.length > 0 && <pre className="log panel">{job.log.join('\n')}</pre>}

      {rep && (
        <>
          {rep.pmTick && rep.pmTick.length > 0 && (
            <section className="panel">
              <div className="panel-head">
                <strong>高频盘口/现货采集连续性（近 24 小时）</strong>
                <span className="muted">存放在独立高频快照库（*-tick.db），用于观测写库争用与断流空档</span>
              </div>
              <table className="table static">
                <thead>
                  <tr>
                    <th>品种</th>
                    <th className="num">样本数</th>
                    <th className="num">P50 间隔</th>
                    <th className="num">P95 间隔</th>
                    <th className="num">P99 间隔</th>
                    <th className="num">最大空档</th>
                    <th className="num">&gt;10s 空档次数</th>
                    <th className="num">连续率</th>
                  </tr>
                </thead>
                <tbody>
                  {rep.pmTick.map((t) => (
                    <tr key={t.symbol}>
                      <td className="mono font-medium">{t.symbol}</td>
                      <td className="num mono">{t.samples.toLocaleString('zh-CN')}</td>
                      <td className="num mono">{(t.p50Ms / 1000).toFixed(2)}s</td>
                      <td className="num mono">{(t.p95Ms / 1000).toFixed(2)}s</td>
                      <td className="num mono">{(t.p99Ms / 1000).toFixed(2)}s</td>
                      <td className={`num mono ${t.maxGapMs > 60000 ? 'bad-text' : ''}`}>{(t.maxGapMs / 1000).toFixed(1)}s</td>
                      <td className={`num mono ${t.gapsOver10s > 0 ? 'warn-text' : ''}`}>{t.gapsOver10s}</td>
                      <td className="num mono">
                        <span className={`tag ${t.uptimeRatio >= 0.99 ? 'tag-ok' : t.uptimeRatio >= 0.95 ? 'tag-warn' : 'tag-bad'}`}>
                          {(t.uptimeRatio * 100).toFixed(2)}%
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>
          )}

          <section className="panel overflow-hidden">
            <div className="panel-head border-b border-[var(--line)]"><strong>新鲜度</strong><span className="muted">应有日期 = 最新交易日向前推发布时滞（两融等 T+1）</span></div>
            <div className="table-scroll">
            <table className="table static">
              <thead><tr><th>数据</th><th>来源</th><th>最新日期</th><th>应有日期</th><th>状态</th></tr></thead>
              <tbody>
                {freshness.map((f) => (
                  <tr key={f.id}>
                    <td><div className="metric-name">{f.name}</div><code className="metric-id">{f.id}</code></td>
                    <td className="muted">{f.source || '—'}</td>
                    <td className="mono">{f.last || '—'}</td>
                    <td className="mono">{f.expected}</td>
                    <td>
                      {f.behind < 0 ? <span className="tag tag-bad">无数据</span>
                        : f.behind === 0 ? <span className="tag tag-ok">最新</span>
                        : <span className={`tag ${f.behind > 2 ? 'tag-bad' : 'tag-warn'}`}>落后 {f.behind} 个交易日</span>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            </div>
          </section>

          <section className="panel">
            <div className="panel-head">
              <strong>按日覆盖</strong>
              <span className="muted">
                最近 {cov.length} 个交易日每天有日线的 A 股数；低于中位数 95% 的日子标为异常
                {lowDays.length === 0 ? '，当前没有异常日' : `，异常 ${lowDays.length} 天`}
              </span>
              <button className="btn btn-ghost" style={{ marginLeft: 'auto' }} onClick={() => setShowAllCov(!showAllCov)}>
                {showAllCov ? '只看异常' : '显示全部'}
              </button>
            </div>
            {covShown.length > 0 && (
              <div className="cov-grid">
                {covShown.map((c) => (
                  <div key={c.date} className={`cov-cell ${c.low ? 'bad' : ''}`}>
                    <span className="mono">{c.date}</span>
                    <strong className="mono">{c.count.toLocaleString('zh-CN')}</strong>
                  </div>
                ))}
              </div>
            )}
          </section>

          <section className="panel">
            <div className="panel-head">
              <strong>个股缺口</strong>
              <span className="muted">
                {rep.windowFrom} 以来，{rep.gapStocks} 只股票在首尾日期之间缺少交易日（合计 {rep.gapDays.toLocaleString('zh-CN')} 个股票·日）。
                停牌也会表现为缺口；点“核对”向数据源确认该日是否有成交。
              </span>
              <input className="gap-filter" placeholder="筛选代码" value={gapQ} onChange={(e) => setGapQ(e.target.value)} />
            </div>
            {gaps.length === 0 ? (
              <div className="empty">{allGaps.length === 0 ? '没有缺口。' : '没有匹配的股票。'}</div>
            ) : (
              <div className="table-scroll scroll-y">
                <table className="table static">
                  <thead><tr><th>代码</th><th className="num">缺失交易日</th><th>缺口区间</th><th></th></tr></thead>
                  <tbody>
                    {gaps.map((g) => (
                      <tr key={g.code}>
                        <td className="mono">{g.code}</td>
                        <td className="num mono">{g.missing}</td>
                        <td className="mono small">
                          {(g.runs ?? []).slice(0, 4).map((r) => (r.from === r.to ? r.from : `${r.from} ~ ${r.to}`) + `（${r.days}）`).join('，')}
                          {(g.runs ?? []).length > 4 && ` 等 ${g.runs.length} 段`}
                        </td>
                        <td className="num"><button className="btn btn-ghost" disabled={running} onClick={() => check([g.code])}>核对</button></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <section className="panel">
            <div className="panel-head">
              <strong>交叉核对</strong>
              <span className="muted">
                {cc
                  ? `${new Date(cc.at).toLocaleString('zh-CN', { hour12: false })}，${cc.codes.length} 只股票，${cc.from} 起；以本地数据为基准，${cc.recentFrom} 及以后的差异记为“近期”（数据源可能仍是临时值）`
                  : '尚未运行。默认抽样 30 只股票（固定包含 600000），比较收盘价、除权乘数与缺失日期。'}
              </span>
            </div>
            {cc && (
              <>
                <div className="table-scroll">
                <table className="table static">
                  <thead>
                    <tr><th>数据源</th><th className="num">股票</th><th className="num">日线</th><th className="num">收盘价不一致</th><th className="num">除权乘数不一致</th><th className="num">本地缺</th><th className="num">来源缺</th><th className="num">近期差异</th><th>说明</th></tr>
                  </thead>
                  <tbody>
                    {(cc.sources ?? []).map((s) => (
                      <tr key={s.source}>
                        <td>{s.source}</td>
                        <td className="num mono">{s.codes}</td>
                        <td className="num mono">{s.compared.toLocaleString('zh-CN')}</td>
                        <td className={`num mono ${s.close ? 'bad-text' : ''}`}>{s.close}</td>
                        <td className={`num mono ${s.mult ? 'bad-text' : ''}`}>{s.mult}</td>
                        <td className={`num mono ${s.onlySource ? 'bad-text' : ''}`}>{s.onlySource}</td>
                        <td className="num mono">{s.onlyLocal}</td>
                        <td className="num mono muted">{s.recent}</td>
                        <td className="muted small">
                          {s.skipped || (s.errors?.length ? `${s.errors.length} 只失败：${s.errors[0]}` : '')}
                          {missingCodes(s.source).length > 0 && (
                            <button className="btn btn-ghost" disabled={running} onClick={() => fill(s.source, missingCodes(s.source))}>
                              用 {s.source} 补齐本地缺失（{missingCodes(s.source).length} 只）
                            </button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                </div>
                {cc.diffs && cc.diffs.length > 0 && (
                  <div className="scroll-y">
                    <table className="table static">
                      <thead><tr><th>代码</th><th>来源</th><th>日期</th><th>差异</th><th className="num">本地</th><th className="num">来源</th></tr></thead>
                      <tbody>
                        {cc.diffs.map((d, i) => (
                          <tr key={i} className={d.recent ? 'muted' : ''}>
                            <td className="mono">{d.code}</td>
                            <td>{d.src}</td>
                            <td className="mono">{d.date}{d.recent && ' · 近期'}</td>
                            <td>{DIFF_TEXT[d.kind] ?? d.kind}</td>
                            <td className="num mono">{d.kind === 'only_source' ? '—' : d.local.toFixed(4)}</td>
                            <td className="num mono">{d.kind === 'only_local' ? '—' : d.source.toFixed(4)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                    {cc.truncated && <p className="disclaimer" style={{ padding: '8px 16px' }}>差异较多，只保留前 300 条。</p>}
                  </div>
                )}
              </>
            )}
          </section>
        </>
      )}
    </div>
  )
}
