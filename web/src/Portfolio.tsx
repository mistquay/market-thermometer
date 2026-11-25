import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, ApiError, type JobStatus, type PortfolioAnalysis, type PortfolioData, type PortfolioItem, type SecurityHit } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { pct } from './temperature'

const KIND_TEXT: Record<string, string> = { stock: '股票', fund: '场内基金', ofund: '场外基金' }

function dir(x: number | null | undefined) {
  if (x === null || x === undefined || Math.abs(x) < 0.0005) return ''
  return x > 0 ? 'up' : 'down'
}

const blank = (): PortfolioData => ({ id: 0, name: '', note: '', start: '', items: [], updatedAt: '' })

export default function Portfolio() {
  const [list, setList] = useState<PortfolioData[]>([])
  const [selected, setSelected] = useState(0)
  const [draft, setDraft] = useState<PortfolioData | null>(null)
  const [analysis, setAnalysis] = useState<PortfolioAnalysis | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(() => api.portfolios().then((l) => {
    setList(l)
    setSelected((cur) => (l.some((p) => p.id === cur) ? cur : l[0]?.id ?? 0))
  }, (e) => setError(e.message)), [])
  useEffect(() => { load() }, [load])

  const analyze = useCallback((id: number) => {
    if (!id) return
    setBusy(true)
    setError('')
    api.analyzePortfolio(id).then(setAnalysis, (e) => { setAnalysis(null); setError(e.message) }).finally(() => setBusy(false))
  }, [])
  useEffect(() => { setAnalysis(null); analyze(selected) }, [selected, analyze])

  const current = list.find((p) => p.id === selected)

  const save = async () => {
    if (!draft) return
    setError('')
    try {
      const saved = await api.savePortfolio(draft)
      setDraft(null)
      await load()
      setSelected(saved.id)
      analyze(saved.id)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }
  const remove = async () => {
    if (!current || !confirm(`删除组合“${current.name}”？`)) return
    await api.deletePortfolio(current.id).catch((e) => setError(e.message))
    setAnalysis(null)
    load()
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>组合</h1>
          <p className="sub">按目标权重在起始日买入后持有（不再平衡），只用本地数据分析收益、回撤、贡献与相关性；基准为沪深300。</p>
        </div>
        <div className="actions">
          <button className="btn btn-primary" onClick={() => setDraft(blank())} disabled={!!draft}>新建组合</button>
        </div>
      </div>
      {error && <div className="error" role="alert">{error}</div>}

      <div className="pf-layout">
        <aside className="panel pf-list">
          {list.length === 0 && <div className="empty">还没有组合。</div>}
          {list.map((p) => (
            <button key={p.id} className={`pf-item ${p.id === selected ? 'on' : ''}`} onClick={() => { setDraft(null); setSelected(p.id) }}>
              <strong>{p.name}</strong>
              <span className="muted small">{p.items.length} 个持仓{p.start && ` · ${p.start} 起`}</span>
            </button>
          ))}
        </aside>

        <div className="pf-main">
          {draft ? (
            <Editor draft={draft} setDraft={setDraft} onSave={save} onCancel={() => setDraft(null)} />
          ) : current ? (
            <>
              <section className="panel">
                <div className="panel-head">
                  <strong>{current.name}</strong>
                  <span className="muted">{current.note}</span>
                  <span style={{ flex: 1 }} />
                  <button className="btn btn-ghost" onClick={() => setDraft(structuredClone(current))}>编辑</button>
                  <button className="btn btn-ghost" onClick={remove}>删除</button>
                </div>
              </section>
              {busy && <div className="panel empty">计算中…</div>}
              {analysis && <Analysis a={analysis} />}
            </>
          ) : null}
        </div>
      </div>
    </div>
  )
}

function Editor({ draft, setDraft, onSave, onCancel }: { draft: PortfolioData; setDraft: (p: PortfolioData) => void; onSave: () => void; onCancel: () => void }) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<SecurityHit[]>([])
  const [names, setNames] = useState(-1)
  const [job, setJob] = useState<JobStatus | null>(null)

  useEffect(() => {
    if (!q.trim()) { setHits([]); return }
    const t = setTimeout(() => api.searchSecurities(q).then((r) => { setHits(r.items); setNames(r.names) }, () => {}), 250)
    return () => clearTimeout(t)
  }, [q])
  useEffect(() => { api.searchSecurities('0').then((r) => setNames(r.names), () => {}) }, [])
  useEffect(() => {
    if (job?.state !== 'running') return
    const t = setInterval(() => api.currentJob().then((j) => {
      setJob(j)
      if (j?.state !== 'running') api.searchSecurities('0').then((r) => setNames(r.names), () => {})
    }, () => {}), 1500)
    return () => clearInterval(t)
  }, [job])

  const total = draft.items.reduce((s, it) => s + (it.weight || 0), 0)
  const add = (h: SecurityHit) => {
    if (draft.items.some((it) => it.kind === h.kind && it.code === h.code)) return
    setDraft({ ...draft, items: [...draft.items, { kind: h.kind, code: h.code, name: h.name, weight: 1 }] })
    setQ('')
    setHits([])
  }
  const setItem = (i: number, patch: Partial<PortfolioItem>) =>
    setDraft({ ...draft, items: draft.items.map((it, k) => (k === i ? { ...it, ...patch } : it)) })

  return (
    <section className="panel pf-editor">
      <div className="panel-head"><strong>{draft.id ? '编辑组合' : '新建组合'}</strong><span className="muted">权重填任意正数，按占比归一化</span></div>
      <div className="form-grid">
        <label className="field"><span>名称</span><input value={draft.name} maxLength={40} onChange={(e) => setDraft({ ...draft, name: e.target.value })} /></label>
        <label className="field"><span>起始日（可空）</span><input type="date" value={draft.start} onChange={(e) => setDraft({ ...draft, start: e.target.value })} /><small className="muted">空为所有持仓都有数据的最早日</small></label>
        <label className="field span-2"><span>备注</span><input value={draft.note} maxLength={500} onChange={(e) => setDraft({ ...draft, note: e.target.value })} /></label>
      </div>
      {names === 0 && (
        <div className="notice">
          还没有股票 / 场内基金名称，只能按代码搜索。
          <button className="btn btn-ghost" disabled={job?.state === 'running'} onClick={() => api.startNames().then(setJob, () => {})}>
            {job?.state === 'running' ? '下载中…' : '下载证券名称'}
          </button>
        </div>
      )}
      <div className="fund-search">
        <input placeholder="输入代码或名称搜索股票、场内基金、场外基金" value={q} onChange={(e) => setQ(e.target.value)} />
        {hits.length > 0 && (
          <ul className="fund-hits">
            {hits.map((h) => (
              <li key={h.kind + h.code}>
                <span className="mono">{h.code}</span> {h.name || '（无名称）'}
                <span className="tag tag-muted">{KIND_TEXT[h.kind]}</span>
                {h.type && <span className="muted small">{h.type}</span>}
                {!h.hasData && <span className="tag tag-warn">本地无数据</span>}
                {h.delisted && <span className="tag tag-muted">已退市</span>}
                <button className="btn btn-ghost" onClick={() => add(h)}>添加</button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {draft.items.length > 0 && (
        <table className="table static">
          <thead><tr><th>证券</th><th>类别</th><th className="num">权重</th><th className="num">占比</th><th /></tr></thead>
          <tbody>
            {draft.items.map((it, i) => (
              <tr key={it.kind + it.code}>
                <td><span className="mono">{it.code}</span> {it.name}</td>
                <td className="muted">{KIND_TEXT[it.kind]}</td>
                <td className="num"><input className="pf-weight" type="number" min={0} step="any" value={it.weight} onChange={(e) => setItem(i, { weight: e.target.valueAsNumber || 0 })} /></td>
                <td className="num mono">{total > 0 ? `${((it.weight / total) * 100).toFixed(1)}%` : '—'}</td>
                <td className="num"><button className="btn btn-ghost" onClick={() => setDraft({ ...draft, items: draft.items.filter((_, k) => k !== i) })}>移除</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="form-foot" style={{ padding: '0 16px 16px' }}>
        <span className="muted small">场外基金会自动加入“基金”页的自选，每日同步净值；新加入的需先同步一次净值才有数据。</span>
        <span style={{ flex: 1 }} />
        <button className="btn" onClick={onCancel}>取消</button>
        <button className="btn btn-primary" disabled={!draft.name.trim() || draft.items.length === 0} onClick={onSave}>保存并分析</button>
      </div>
    </section>
  )
}

function Analysis({ a }: { a: PortfolioAnalysis }) {
  const r = a.result
  const labels = useMemo(() => Object.fromEntries(r.assets.map((x) => [x.key, x.label])), [r.assets])
  const navOption = useMemo<EChartsOption | null>(() => {
    if (r.dates.length === 0) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line'), accent = cssVar('--accent'), bad = cssVar('--bad')
    return {
      animation: false,
      tooltip: { trigger: 'axis', valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(4) : String(v)) },
      legend: { top: 0, left: 0, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
      axisPointer: { link: [{ xAxisIndex: 'all' }] },
      grid: [{ left: 56, right: 24, top: 32, height: '58%' }, { left: 56, right: 24, top: '74%', bottom: 48 }],
      xAxis: [
        { type: 'category', data: r.dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { show: false } },
        { type: 'category', gridIndex: 1, data: r.dates, boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      ],
      yAxis: [
        { type: 'value', scale: true, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
        { type: 'value', gridIndex: 1, max: 0, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink, formatter: (v: number) => `${Math.round(v * 100)}%` } },
      ],
      dataZoom: [{ type: 'inside', xAxisIndex: [0, 1] }, { type: 'slider', xAxisIndex: [0, 1], height: 16, bottom: 8, borderColor: line, textStyle: { color: ink } }],
      series: [
        { name: '组合净值', type: 'line', data: r.nav, showSymbol: false, lineStyle: { width: 1.8, color: accent }, itemStyle: { color: accent } },
        { name: '沪深300', type: 'line', data: r.benchNav, showSymbol: false, lineStyle: { width: 1.1, color: ink }, itemStyle: { color: ink } },
        { name: '回撤', type: 'line', xAxisIndex: 1, yAxisIndex: 1, data: r.drawdown, showSymbol: false, lineStyle: { width: 1, color: bad }, itemStyle: { color: bad }, areaStyle: { color: bad, opacity: 0.15 } },
      ],
    }
  }, [r])
  const corrOption = useMemo<EChartsOption | null>(() => {
    if (r.corrKeys.length < 2) return null
    const ink = cssVar('--ink-2')
    const names = r.corrKeys.map((k) => labels[k] || k)
    const data: [number, number, number | null][] = []
    r.corr.forEach((row, i) => row.forEach((v, j) => data.push([j, i, v === null ? null : Math.round(v * 100) / 100])))
    return {
      animation: false,
      tooltip: { formatter: (p: unknown) => { const d = (p as { data: [number, number, number] }).data; return `${names[d[1]]} × ${names[d[0]]}：${d[2]}` } },
      grid: { left: 120, right: 24, top: 8, bottom: 80 },
      xAxis: { type: 'category', data: names, axisLabel: { color: ink, rotate: 30 } },
      yAxis: { type: 'category', data: names, axisLabel: { color: ink } },
      visualMap: { min: -1, max: 1, calculable: false, orient: 'horizontal', left: 'center', bottom: 0, itemHeight: 120, textStyle: { color: ink },
        inRange: { color: [cssVar('--t-cold-2'), cssVar('--panel'), cssVar('--t-hot-2')] } },
      series: [{ type: 'heatmap', data, label: { show: names.length <= 10, color: cssVar('--ink') } }],
    }
  }, [r, labels])

  if (r.note) return <div className="panel empty">{r.note}{r.missing.length > 0 && `（缺数据：${r.missing.join('、')}）`}</div>
  const s = r.stats, b = r.bench
  const tiles: [string, string, string][] = [
    ['总收益', pct(s.totalRet), `沪深300 ${pct(b.totalRet)}`],
    ['年化收益', pct(s.cagr), `超额 ${pct(s.cagr - b.cagr)}`],
    ['年化波动', pct(s.vol), `沪深300 ${pct(b.vol)}`],
    ['最大回撤', pct(s.maxDD), `沪深300 ${pct(b.maxDD)}`],
    ['夏普（无风险利率 0）', s.sharpe === null ? '—' : s.sharpe.toFixed(2), `沪深300 ${b.sharpe === null ? '—' : b.sharpe.toFixed(2)}`],
    ['Beta / 相关系数', `${r.beta === null ? '—' : r.beta.toFixed(2)} / ${r.benchCorr === null ? '—' : r.benchCorr.toFixed(2)}`, '相对沪深300 日收益'],
  ]
  return (
    <>
      {r.assets.some((x) => x.stale) && (
        <div className="notice">
          以下持仓的本地数据早于 {r.to} 结束（退市、停更或尚未同步），之后的日子按最后价格计算：
          {r.assets.filter((x) => x.stale).map((x) => `${x.label}（止于 ${x.last}）`).join('、')}
        </div>
      )}
      {r.missing.length > 0 && <div className="notice">以下持仓本地没有数据，未计入（其余持仓按权重重新归一化）：{r.missing.map((k) => k.split(':')[1]).join('、')}</div>}
      <div className="pf-tiles">
        {tiles.map(([k, v, sub]) => (
          <div key={k} className="panel pf-tile"><span className="muted small">{k}</span><strong className="mono">{v}</strong><span className="muted small">{sub}</span></div>
        ))}
      </div>
      <section className="panel chart-panel">
        <div className="panel-head"><strong>净值与回撤</strong><span className="muted">{r.from} ~ {r.to}，{r.dates.length} 个交易日，耗时 {a.elapsedMs} ms</span></div>
        {navOption && <EChart option={navOption} height={420} />}
      </section>
      <section className="panel">
        <div className="panel-head"><strong>持仓</strong><span className="muted">买入持有：贡献 = 初始权重 × 区间收益，合计等于组合总收益；期末权重因涨跌漂移</span></div>
        <table className="table static">
          <thead><tr><th>证券</th><th className="num">初始权重</th><th className="num">期末权重</th><th className="num">区间收益</th><th className="num">贡献</th><th className="num">年化波动</th><th className="num">最大回撤</th><th>本地数据</th></tr></thead>
          <tbody>
            {r.assets.map((x) => (
              <tr key={x.key}>
                <td><a className="mono" href={`#/quote/${x.key.replace(':', '/')}`}>{x.key.split(':')[1]}</a> {x.label !== x.key.split(':')[1] && x.label} <span className="tag tag-muted">{KIND_TEXT[x.key.split(':')[0]]}</span></td>
                <td className="num mono">{(x.weight * 100).toFixed(1)}%</td>
                <td className="num mono">{(x.endWeight * 100).toFixed(1)}%</td>
                <td className={`num mono ${dir(x.totalRet)}`}>{pct(x.totalRet)}</td>
                <td className={`num mono ${dir(x.contribution)}`}>{pct(x.contribution)}</td>
                <td className="num mono">{pct(x.vol)}</td>
                <td className="num mono">{pct(x.maxDD)}</td>
                <td className="mono muted nowrap">
                  {x.first} ~ {x.last}
                  {x.stale && <div className="bad-text small">数据止于 {x.last}，之后按最后价格计算</div>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
      <div className="pf-two">
        {corrOption && (
          <section className="panel">
            <div className="panel-head"><strong>相关矩阵</strong><span className="muted">日收益 Pearson 相关</span></div>
            <EChart option={corrOption} height={Math.max(260, r.corrKeys.length * 34 + 110)} />
          </section>
        )}
        <section className="panel">
          <div className="panel-head"><strong>分年度</strong></div>
          <table className="table static">
            <thead><tr><th>年份</th><th className="num">组合</th><th className="num">沪深300</th><th className="num">超额</th></tr></thead>
            <tbody>
              {r.years.map((y) => (
                <tr key={y.year}>
                  <td>{y.year}</td>
                  <td className={`num mono ${dir(y.ret)}`}>{pct(y.ret)}</td>
                  <td className={`num mono ${dir(y.bench)}`}>{pct(y.bench)}</td>
                  <td className={`num mono ${dir(y.excess)}`}>{pct(y.excess)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      </div>
      <p className="disclaimer">
        口径：股票、场内基金用不复权收盘价与除权乘数链式算出的后复权价（ETF 分红没有除权标注，收益会被低估）；场外基金用累计净值（T+1 公布）；
        停牌日价格不变。基准沪深300 是价格指数、不含分红，而持仓收益含分红，超额收益每年约高估 2–3 个百分点。只描述历史表现，不构成投资建议。
      </p>
    </>
  )
}
