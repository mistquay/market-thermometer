import { useCallback, useEffect, useState } from 'react'
import { Download, Filter, RefreshCw, Star } from 'lucide-react'
import { api, type ScreenerResp, type ScreenerRow } from './api'

type FilterState = {
  kind: 'stock' | 'fund' | 'all'
  q: string
  peMin: string
  peMax: string
  pbMin: string
  pbMax: string
  pePctMax: string
  pbPctMax: string
  mvMin: string
  mvMax: string
  amtMin: string
  aboveMa200: '' | '1' | '0'
  watched: boolean
  ret20Min: string
  ret20Max: string
  sort: string
  desc: '1' | '0'
  limit: number
}

const INIT: FilterState = {
  kind: 'stock',
  q: '',
  peMin: '',
  peMax: '',
  pbMin: '',
  pbMax: '',
  pePctMax: '',
  pbPctMax: '',
  mvMin: '50',
  mvMax: '',
  amtMin: '',
  aboveMa200: '',
  watched: false,
  ret20Min: '',
  ret20Max: '',
  sort: 'mv',
  desc: '1',
  limit: 100,
}

const PRESETS: { label: string; patch: Partial<FilterState> }[] = [
  { label: '大盘低估值（市值≥200亿 · PE≤15）', patch: { kind: 'stock', mvMin: '200', peMin: '0', peMax: '15', pePctMax: '', pbMax: '', aboveMa200: '', sort: 'pe', desc: '0' } },
  { label: '历史低位估值（5年PE分位≤20% · 市值≥100亿）', patch: { kind: 'stock', mvMin: '100', peMin: '0', peMax: '', pePctMax: '20', pbMax: '', aboveMa200: '', sort: 'pePct', desc: '0' } },
  { label: '站上 MA200 强势（市值≥100亿）', patch: { kind: 'stock', mvMin: '100', peMin: '', peMax: '', pePctMax: '', pbMax: '', aboveMa200: '1', sort: 'ret20', desc: '1' } },
  { label: '深度破净（PB≤0.8 · PE>0）', patch: { kind: 'stock', mvMin: '50', peMin: '0', peMax: '', pePctMax: '', pbMin: '0', pbMax: '0.8', aboveMa200: '', sort: 'pb', desc: '0' } },
  { label: '场内 ETF 动量榜', patch: { kind: 'fund', mvMin: '', peMin: '', peMax: '', pePctMax: '', pbMin: '', pbMax: '', aboveMa200: '', sort: 'ret20', desc: '1' } },
]

const inputCls =
  'h-8 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-2.5 text-xs mono text-[var(--ink)] focus:border-[var(--accent)] focus:outline-none'

const fmtPct = (v: number | null) =>
  v === null ? '—' : <span className={v > 0 ? 'up' : v < 0 ? 'down' : ''}>{`${v > 0 ? '+' : ''}${(v * 100).toFixed(1)}%`}</span>

export default function Screener() {
  const [f, setF] = useState<FilterState>(INIT)
  const [data, setData] = useState<ScreenerResp | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const run = useCallback((st: FilterState, refresh = false) => {
    setBusy(true)
    setError('')
    const params: Record<string, string> = { kind: st.kind, sort: st.sort, desc: st.desc, limit: String(st.limit) }
    if (st.q.trim()) params.q = st.q.trim()
    if (st.peMin) params.peMin = st.peMin
    if (st.peMax) params.peMax = st.peMax
    if (st.pbMin) params.pbMin = st.pbMin
    if (st.pbMax) params.pbMax = st.pbMax
    if (st.pePctMax) params.pePctMax = st.pePctMax
    if (st.pbPctMax) params.pbPctMax = st.pbPctMax
    if (st.mvMin) params.mvMin = st.mvMin
    if (st.mvMax) params.mvMax = st.mvMax
    if (st.amtMin) params.amtMin = st.amtMin
    if (st.aboveMa200) params.aboveMa200 = st.aboveMa200
    if (st.watched) params.watched = '1'
    if (st.ret20Min) params.ret20Min = st.ret20Min
    if (st.ret20Max) params.ret20Max = st.ret20Max
    if (refresh) params.refresh = '1'
    api
      .screener(params)
      .then(setData, (e: Error) => setError(e.message))
      .finally(() => setBusy(false))
  }, [])

  useEffect(() => {
    run(INIT)
  }, [run])

  const applyPreset = (patch: Partial<FilterState>) => {
    const next = { ...INIT, ...patch }
    setF(next)
    run(next)
  }

  const toggleSort = (key: string) => {
    const next: FilterState = { ...f, sort: key, desc: f.sort === key && f.desc === '1' ? '0' : '1' }
    setF(next)
    run(next)
  }

  const toggleWatch = (r: ScreenerRow, e: React.MouseEvent) => {
    e.stopPropagation()
    const op = r.watched ? api.unwatchStock(r.kind, r.code) : api.watchStock(r.kind, r.code)
    op.then(
      () => {
        setData((cur) =>
          cur
            ? {
                ...cur,
                watched: (cur.watched ?? 0) + (r.watched ? -1 : 1),
                items: cur.items.map((x) => (x.kind === r.kind && x.code === r.code ? { ...x, watched: !x.watched } : x)),
              }
            : cur,
        )
      },
      (err: Error) => setError(err.message),
    )
  }

  const exportCSV = () => {
    if (!data?.items?.length) return
    const head = ['代码', '类别', '名称', '日期', '收盘价', '总市值(亿)', 'PE(TTM)', '5年PE分位(%)', 'PB', '5年PB分位(%)', '20日涨跌(%)', '60日涨跌(%)', '相对MA200(%)', '成交额(万)']
    const esc = (v: unknown) => `"${String(v ?? '').replace(/"/g, '""')}"`
    const rows = data.items.map((r) => [
      r.code,
      r.kind,
      r.name,
      r.date,
      r.close.toFixed(2),
      r.totalMv?.toFixed(2) ?? '',
      r.peTtm?.toFixed(2) ?? '',
      r.pePct5Y?.toFixed(1) ?? '',
      r.pb?.toFixed(2) ?? '',
      r.pbPct5Y?.toFixed(1) ?? '',
      r.ret20 !== null ? (r.ret20 * 100).toFixed(2) : '',
      r.ret60 !== null ? (r.ret60 * 100).toFixed(2) : '',
      r.distMa200 !== null ? (r.distMa200 * 100).toFixed(2) : '',
      r.amount?.toFixed(1) ?? '',
    ])
    const csv = '\uFEFF' + [head, ...rows].map((line) => line.map(esc).join(',')).join('\n')
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `screener-${data.asOf || 'export'}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  const sortMark = (key: string) => (f.sort === key ? (f.desc === '1' ? ' ↓' : ' ↑') : '')

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>本地选股器</h1>
          <p className="sub">
            纯本地计算（基于已同步的日线与扩展估值表，含近 5 年 PE/PB 历史分位），支持星标自选与 CSV 导出。
            {data && (
              <span className="muted mono">
                {' '}
                数据截至 {data.asOf || '—'} · 全市场 {data.universe.toLocaleString()} 只 · 命中 {data.total.toLocaleString()} 只 · 自选 {data.watched ?? 0} 只（{data.elapsedMs}ms）
              </span>
            )}
          </p>
        </div>
        <div className="actions">
          <button className="btn" disabled={!data?.items?.length} onClick={exportCSV}>
            <Download className="h-3.5 w-3.5" />
            导出 CSV
          </button>
          <button className="btn" disabled={busy} onClick={() => run(f, true)}>
            <RefreshCw className={`h-3.5 w-3.5 ${busy ? 'animate-spin' : ''}`} />
            重算缓存
          </button>
          <button className="btn btn-primary" disabled={busy} onClick={() => run(f)}>
            <Filter className="h-3.5 w-3.5" />
            {busy ? '筛选中…' : '执行筛选'}
          </button>
        </div>
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      {/* 快捷预设 + 筛选条件 */}
      <section className="panel overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-[var(--line)] bg-[var(--bg-subtle)] px-4 py-2.5">
          <span className="text-xs font-medium text-[var(--ink-2)]">常用筛选：</span>
          {PRESETS.map((p) => (
            <button key={p.label} type="button" className="btn btn-ghost text-xs" onClick={() => applyPreset(p.patch)}>
              {p.label}
            </button>
          ))}
          <label className="ml-auto flex items-center gap-1.5 text-xs font-medium text-[var(--ink)]">
            <input
              type="checkbox"
              checked={f.watched}
              onChange={(e) => {
                const next = { ...f, watched: e.target.checked, mvMin: e.target.checked ? '' : f.mvMin }
                setF(next)
                run(next)
              }}
            />
            仅看自选 ({data?.watched ?? 0})
          </label>
        </div>

        <div className="grid grid-cols-2 gap-3 p-4 sm:grid-cols-4 lg:grid-cols-7">
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>证券类别</span>
            <select className={inputCls} value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value as FilterState['kind'] })}>
              <option value="stock">A 股股票</option>
              <option value="fund">场内基金（ETF/LOF）</option>
              <option value="all">全部</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>代码 / 名称</span>
            <input className={inputCls} placeholder="如 600519 或 银行" value={f.q} onChange={(e) => setF({ ...f, q: e.target.value })} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>总市值 ≥ / ≤（亿）</span>
            <div className="flex gap-1.5">
              <input className={`${inputCls} w-full`} placeholder="最小" value={f.mvMin} onChange={(e) => setF({ ...f, mvMin: e.target.value })} />
              <input className={`${inputCls} w-full`} placeholder="最大" value={f.mvMax} onChange={(e) => setF({ ...f, mvMax: e.target.value })} />
            </div>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>PE (TTM) 区间</span>
            <div className="flex gap-1.5">
              <input className={`${inputCls} w-full`} placeholder="最小" value={f.peMin} onChange={(e) => setF({ ...f, peMin: e.target.value })} />
              <input className={`${inputCls} w-full`} placeholder="最大" value={f.peMax} onChange={(e) => setF({ ...f, peMax: e.target.value })} />
            </div>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>PB 区间</span>
            <div className="flex gap-1.5">
              <input className={`${inputCls} w-full`} placeholder="最小" value={f.pbMin} onChange={(e) => setF({ ...f, pbMin: e.target.value })} />
              <input className={`${inputCls} w-full`} placeholder="最大" value={f.pbMax} onChange={(e) => setF({ ...f, pbMax: e.target.value })} />
            </div>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>5年分位上限 (%)</span>
            <div className="flex gap-1.5">
              <input className={`${inputCls} w-full`} placeholder="PE≤%" value={f.pePctMax} onChange={(e) => setF({ ...f, pePctMax: e.target.value })} />
              <input className={`${inputCls} w-full`} placeholder="PB≤%" value={f.pbPctMax} onChange={(e) => setF({ ...f, pbPctMax: e.target.value })} />
            </div>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>MA200 位置</span>
            <select className={inputCls} value={f.aboveMa200} onChange={(e) => setF({ ...f, aboveMa200: e.target.value as FilterState['aboveMa200'] })}>
              <option value="">不限</option>
              <option value="1">站上 200 日线</option>
              <option value="0">低于 200 日线</option>
            </select>
          </label>
        </div>
      </section>

      {/* 结果表 */}
      <section className="panel overflow-hidden">
        <div className="table-scroll">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 32 }}></th>
                <th>代码 / 名称</th>
                <th className="num cursor-pointer" onClick={() => toggleSort('close')}>
                  收盘价{sortMark('close')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('mv')}>
                  总市值(亿){sortMark('mv')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('pe')}>
                  PE(TTM){sortMark('pe')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('pePct')}>
                  5年PE分位{sortMark('pePct')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('pb')}>
                  PB{sortMark('pb')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('pbPct')}>
                  5年PB分位{sortMark('pbPct')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('ret20')}>
                  20日涨跌{sortMark('ret20')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('ret60')}>
                  60日涨跌{sortMark('ret60')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('distMa200')}>
                  相对MA200{sortMark('distMa200')}
                </th>
                <th className="num cursor-pointer" onClick={() => toggleSort('amount')}>
                  成交额(万){sortMark('amount')}
                </th>
              </tr>
            </thead>
            <tbody>
              {(data?.items ?? []).map((r: ScreenerRow) => (
                <tr
                  key={`${r.kind}-${r.code}`}
                  onClick={() => {
                    location.hash = `#/quote/${r.kind}/${r.code}`
                  }}
                >
                  <td>
                    <button
                      type="button"
                      className="cursor-pointer border-0 bg-transparent p-1 text-[var(--ink-3)] hover:text-[var(--warn)]"
                      title={r.watched ? '取消自选' : '加入自选'}
                      onClick={(e) => toggleWatch(r, e)}
                    >
                      <Star className={`h-3.5 w-3.5 ${r.watched ? 'fill-[var(--warn)] text-[var(--warn)]' : ''}`} />
                    </button>
                  </td>
                  <td>
                    <span className="mono font-medium text-[var(--accent)]">{r.code}</span>
                    <span className="ml-2">{r.name || '—'}</span>
                  </td>
                  <td className="num mono">{r.close.toFixed(2)}</td>
                  <td className="num mono">{r.totalMv !== null ? r.totalMv.toFixed(1) : '—'}</td>
                  <td className="num mono">{r.peTtm !== null ? r.peTtm.toFixed(1) : '—'}</td>
                  <td className={`num mono small ${r.pePct5Y !== null && r.pePct5Y <= 25 ? 'up' : r.pePct5Y !== null && r.pePct5Y >= 75 ? 'down' : ''}`}>
                    {r.pePct5Y !== null ? `${r.pePct5Y.toFixed(0)}%` : '—'}
                  </td>
                  <td className="num mono">{r.pb !== null ? r.pb.toFixed(2) : '—'}</td>
                  <td className={`num mono small ${r.pbPct5Y !== null && r.pbPct5Y <= 25 ? 'up' : r.pbPct5Y !== null && r.pbPct5Y >= 75 ? 'down' : ''}`}>
                    {r.pbPct5Y !== null ? `${r.pbPct5Y.toFixed(0)}%` : '—'}
                  </td>
                  <td className="num mono">{fmtPct(r.ret20)}</td>
                  <td className="num mono">{fmtPct(r.ret60)}</td>
                  <td className="num mono">
                    {r.distMa200 === null ? (
                      '—'
                    ) : (
                      <span className={`tag ${r.aboveMa200 ? 'tag-ok' : 'tag-muted'}`}>
                        {r.distMa200 >= 0 ? '+' : ''}
                        {(r.distMa200 * 100).toFixed(1)}%
                      </span>
                    )}
                  </td>
                  <td className="num mono text-[var(--ink-2)]">{r.amount !== null ? r.amount.toLocaleString('zh-CN', { maximumFractionDigits: 0 }) : '—'}</td>
                </tr>
              ))}
              {data && data.items.length === 0 && (
                <tr>
                  <td colSpan={12} className="muted">
                    没有符合条件的证券。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  )
}
