import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { CandlestickChart, ChevronLeft, ChevronRight, Download, Filter, RefreshCw, RotateCcw, Star, Wallet } from 'lucide-react'
import { api, type ScreenerResp, type ScreenerRow } from './api'
import { useToast } from './ui/Feedback'

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
  limit: 200,
}

const PRESETS: { id: string; label: string; patch: Partial<FilterState> }[] = [
  { id: 'big-value', label: '大盘低估值 (市值≥200亿 · PE≤15)', patch: { kind: 'stock', mvMin: '200', peMin: '0', peMax: '15', pePctMax: '', pbMax: '', aboveMa200: '', sort: 'pe', desc: '0' } },
  { id: 'low-pct', label: '历史低位估值 (5Y PE分位≤20%)', patch: { kind: 'stock', mvMin: '100', peMin: '0', peMax: '', pePctMax: '20', pbMax: '', aboveMa200: '', sort: 'pePct', desc: '0' } },
  { id: 'ma200-up', label: '站上 MA200 强势 (市值≥100亿)', patch: { kind: 'stock', mvMin: '100', peMin: '', peMax: '', pePctMax: '', pbMax: '', aboveMa200: '1', sort: 'ret20', desc: '1' } },
  { id: 'deep-pb', label: '深度破净 (PB≤0.8 · PE>0)', patch: { kind: 'stock', mvMin: '50', peMin: '0', peMax: '', pePctMax: '', pbMin: '0', pbMax: '0.8', aboveMa200: '', sort: 'pb', desc: '0' } },
  { id: 'etf-mom', label: '场内 ETF 动量榜', patch: { kind: 'fund', mvMin: '', peMin: '', peMax: '', pePctMax: '', pbMin: '', pbMax: '', aboveMa200: '', sort: 'ret20', desc: '1' } },
]

const inputCls =
  'h-8.5 w-full rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-2.5 text-xs mono text-[var(--ink)] placeholder:text-[var(--ink-3)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[var(--accent-soft)] focus:outline-none'

const fmtPct = (v: number | null) =>
  v === null ? '—' : <span className={v > 0 ? 'up' : v < 0 ? 'down' : ''}>{`${v > 0 ? '+' : ''}${(v * 100).toFixed(1)}%`}</span>

function ValPctCell({ value }: { value: number | null }) {
  if (value === null) return <span className="muted">—</span>
  const color = value <= 25 ? 'var(--t-cold-2)' : value >= 75 ? 'var(--t-hot-2)' : 'var(--ink-2)'
  return (
    <div className="ml-auto flex w-14 flex-col items-end gap-0.5">
      <span className="mono text-xs font-medium" style={{ color }}>
        {value.toFixed(0)}%
      </span>
      <div className="h-1 w-full overflow-hidden rounded-full bg-[var(--bg-subtle)]">
        <div className="h-full rounded-full" style={{ width: `${Math.min(100, Math.max(0, value))}%`, background: color }} />
      </div>
    </div>
  )
}

export default function Screener() {
  const [f, setF] = useState<FilterState>(INIT)
  const [activePreset, setActivePreset] = useState<string>('')
  const [data, setData] = useState<ScreenerResp | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(25)
  const mounted = useRef(false)
  const toast = useToast()

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
      .then((res) => {
        setData(res)
        setPage(1)
      }, (e: Error) => setError(e.message))
      .finally(() => setBusy(false))
  }, [])

  // 首次加载 + 筛选条件防抖即时反馈
  useEffect(() => {
    if (!mounted.current) {
      mounted.current = true
      run(f)
      return
    }
    const t = window.setTimeout(() => run(f), 320)
    return () => window.clearTimeout(t)
  }, [f, run])

  const patchFilter = (patch: Partial<FilterState>, clearPreset = true) => {
    if (clearPreset) setActivePreset('')
    setF((cur) => ({ ...cur, ...patch }))
  }

  const applyPreset = (id: string, patch: Partial<FilterState>) => {
    const next = { ...INIT, ...patch }
    setActivePreset(id)
    setF(next)
  }

  const resetFilter = () => {
    setActivePreset('')
    setF(INIT)
  }

  const toggleSort = (key: string) => {
    setF((cur) => ({ ...cur, sort: key, desc: cur.sort === key && cur.desc === '1' ? '0' : '1' }))
  }

  const toggleWatch = (r: ScreenerRow, e: React.MouseEvent) => {
    e.stopPropagation()
    const op = r.watched ? api.unwatchStock(r.kind, r.code) : api.watchStock(r.kind, r.code)
    op.then(
      () => {
        toast.success(r.watched ? `已取消自选 ${r.code} ${r.name || ''}` : `已加入自选 ${r.code} ${r.name || ''}`)
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
      (err: Error) => toast.error(err.message),
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
    toast.success(`已导出 ${data.items.length} 条筛选结果`)
  }

  const sortMark = (key: string) => (f.sort === key ? (f.desc === '1' ? ' ↓' : ' ↑') : '')
  const allItems = data?.items ?? []
  const totalPages = Math.max(1, Math.ceil(allItems.length / pageSize))
  const curPage = Math.min(page, totalPages)
  const pageItems = useMemo(
    () => allItems.slice((curPage - 1) * pageSize, curPage * pageSize),
    [allItems, curPage, pageSize],
  )

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>本地选股器</h1>
          <p className="sub">
            纯本地计算（含近 5 年 PE/PB 历史分位），支持即时筛选、星标自选、CSV 导出与直达模拟盘下单。
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
            {busy ? '筛选中…' : '立即筛选'}
          </button>
        </div>
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      {/* 快捷预设药丸 + 呼吸感 2 行筛选网格 */}
      <section className="panel overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-[var(--line)] bg-[var(--bg-subtle)] px-4 py-3">
          <span className="text-xs font-semibold text-[var(--ink-2)]">常用预设</span>
          {PRESETS.map((p) => {
            const on = activePreset === p.id
            return (
              <button
                key={p.id}
                type="button"
                aria-pressed={on}
                onClick={() => applyPreset(p.id, p.patch)}
                className={`cursor-pointer rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
                  on
                    ? 'border-[var(--accent)] bg-[var(--accent-soft)] text-[var(--accent)]'
                    : 'border-[var(--line-strong)] bg-[var(--panel)] text-[var(--ink-2)] hover:border-[var(--ink-3)] hover:text-[var(--ink)]'
                }`}
              >
                {p.label}
              </button>
            )
          })}
          <div className="ml-auto flex items-center gap-3">
            <label className="flex cursor-pointer items-center gap-1.5 text-xs font-medium text-[var(--ink)]">
              <input
                type="checkbox"
                className="accent-[var(--accent)]"
                checked={f.watched}
                onChange={(e) => patchFilter({ watched: e.target.checked, mvMin: e.target.checked ? '' : f.mvMin })}
              />
              仅看自选 ({data?.watched ?? 0})
            </label>
            <button type="button" className="btn btn-ghost h-7 px-2 text-xs" onClick={resetFilter} title="重置为默认条件">
              <RotateCcw className="h-3 w-3" />
              重置
            </button>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-3.5 p-4 sm:grid-cols-2 lg:grid-cols-4">
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>证券类别</span>
            <select className={inputCls} value={f.kind} onChange={(e) => patchFilter({ kind: e.target.value as FilterState['kind'] })}>
              <option value="stock">A 股股票</option>
              <option value="fund">场内基金（ETF/LOF）</option>
              <option value="all">全部</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>代码 / 名称搜索</span>
            <input className={inputCls} placeholder="如 600519 或 银行（自动筛选）" value={f.q} onChange={(e) => patchFilter({ q: e.target.value })} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>总市值区间（亿）</span>
            <div className="grid grid-cols-2 gap-2">
              <input className={inputCls} placeholder="最小市值" value={f.mvMin} onChange={(e) => patchFilter({ mvMin: e.target.value })} />
              <input className={inputCls} placeholder="最大市值" value={f.mvMax} onChange={(e) => patchFilter({ mvMax: e.target.value })} />
            </div>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>MA200 均线位置</span>
            <select className={inputCls} value={f.aboveMa200} onChange={(e) => patchFilter({ aboveMa200: e.target.value as FilterState['aboveMa200'] })}>
              <option value="">不限</option>
              <option value="1">站上 200 日线</option>
              <option value="0">低于 200 日线</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>PE (TTM) 区间</span>
            <div className="grid grid-cols-2 gap-2">
              <input className={inputCls} placeholder="最小 PE" value={f.peMin} onChange={(e) => patchFilter({ peMin: e.target.value })} />
              <input className={inputCls} placeholder="最大 PE" value={f.peMax} onChange={(e) => patchFilter({ peMax: e.target.value })} />
            </div>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>PB 市净率区间</span>
            <div className="grid grid-cols-2 gap-2">
              <input className={inputCls} placeholder="最小 PB" value={f.pbMin} onChange={(e) => patchFilter({ pbMin: e.target.value })} />
              <input className={inputCls} placeholder="最大 PB" value={f.pbMax} onChange={(e) => patchFilter({ pbMax: e.target.value })} />
            </div>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>5年历史分位上限 (%)</span>
            <div className="grid grid-cols-2 gap-2">
              <input className={inputCls} placeholder="PE分位 ≤%" value={f.pePctMax} onChange={(e) => patchFilter({ pePctMax: e.target.value })} />
              <input className={inputCls} placeholder="PB分位 ≤%" value={f.pbPctMax} onChange={(e) => patchFilter({ pbPctMax: e.target.value })} />
            </div>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>色标口径说明</span>
            <div className="flex h-8.5 items-center gap-2 rounded-lg border border-[var(--line)] bg-[var(--bg-subtle)]/50 px-2.5 text-[11px] text-[var(--ink-2)]">
              <span className="val-cold font-medium">蓝=低估分位</span>
              <span>·</span>
              <span className="val-hot font-medium">红=高估分位</span>
              <span>·</span>
              <span>涨跌红涨绿跌</span>
            </div>
          </label>
        </div>
      </section>

      {/* 结果表（吸顶表头 + 分页 + 快捷下单/K线） */}
      <section className="panel overflow-hidden">
        <div className="table-scroll scroll-y" style={{ maxHeight: 620 }}>
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 34 }}></th>
                <th className="nowrap">代码 / 名称</th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('close')}>
                  收盘价{sortMark('close')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('mv')}>
                  总市值(亿){sortMark('mv')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('pe')}>
                  PE(TTM){sortMark('pe')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('pePct')}>
                  5年PE分位{sortMark('pePct')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('pb')}>
                  PB{sortMark('pb')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('pbPct')}>
                  5年PB分位{sortMark('pbPct')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('ret20')}>
                  20日涨跌{sortMark('ret20')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('ret60')}>
                  60日涨跌{sortMark('ret60')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('distMa200')}>
                  相对MA200{sortMark('distMa200')}
                </th>
                <th className="num nowrap cursor-pointer" onClick={() => toggleSort('amount')}>
                  成交额(万){sortMark('amount')}
                </th>
                <th className="num nowrap">快捷</th>
              </tr>
            </thead>
            <tbody>
              {pageItems.map((r: ScreenerRow) => (
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
                  <td className="nowrap">
                    <span className="mono font-medium text-[var(--accent)]">{r.code}</span>
                    <span className="ml-2 font-medium text-[var(--ink)]">{r.name || '—'}</span>
                  </td>
                  <td className="num mono nowrap">{r.close.toFixed(2)}</td>
                  <td className="num mono nowrap">{r.totalMv !== null ? r.totalMv.toFixed(1) : '—'}</td>
                  <td className="num mono nowrap">{r.peTtm !== null ? r.peTtm.toFixed(1) : '—'}</td>
                  <td className="num nowrap">
                    <ValPctCell value={r.pePct5Y} />
                  </td>
                  <td className="num mono nowrap">{r.pb !== null ? r.pb.toFixed(2) : '—'}</td>
                  <td className="num nowrap">
                    <ValPctCell value={r.pbPct5Y} />
                  </td>
                  <td className="num mono nowrap">{fmtPct(r.ret20)}</td>
                  <td className="num mono nowrap">{fmtPct(r.ret60)}</td>
                  <td className="num mono nowrap">
                    {r.distMa200 === null ? (
                      '—'
                    ) : (
                      <span className={r.distMa200 >= 0 ? 'up font-medium' : 'down font-medium'}>
                        {r.distMa200 >= 0 ? '+' : ''}
                        {(r.distMa200 * 100).toFixed(1)}%
                      </span>
                    )}
                  </td>
                  <td className="num mono nowrap text-[var(--ink-2)]">
                    {r.amount !== null ? r.amount.toLocaleString('zh-CN', { maximumFractionDigits: 0 }) : '—'}
                  </td>
                  <td className="num nowrap" onClick={(e) => e.stopPropagation()}>
                    <div className="inline-flex items-center gap-1">
                      <a
                        href={`#/quote/${r.kind}/${r.code}`}
                        className="btn btn-ghost h-7 px-1.5 text-xs"
                        title="查看 K 线与估值"
                      >
                        <CandlestickChart className="h-3.5 w-3.5" />
                      </a>
                      <a
                        href={`#/sim?code=${encodeURIComponent(r.code)}&name=${encodeURIComponent(r.name || '')}`}
                        className="btn btn-ghost h-7 px-1.5 text-xs"
                        title="带入模拟盘下单"
                      >
                        <Wallet className="h-3.5 w-3.5" />
                      </a>
                    </div>
                  </td>
                </tr>
              ))}
              {data && data.items.length === 0 && (
                <tr>
                  <td colSpan={13} className="empty">
                    没有符合条件的证券，可尝试放宽市值或估值分位限制。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        {/* 分页条 */}
        {allItems.length > 0 && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--line)] bg-[var(--bg-subtle)] px-4 py-2.5 text-xs text-[var(--ink-2)]">
            <span className="mono">
              显示 {(curPage - 1) * pageSize + 1}–{Math.min(curPage * pageSize, allItems.length)} / 共 {allItems.length} 条
              {data && data.total > allItems.length ? `（总命中 ${data.total} 条，按当前排序取前 ${f.limit} 条）` : ''}
            </span>
            <div className="flex items-center gap-2">
              <select
                className="h-7 rounded-md border border-[var(--line-strong)] bg-[var(--panel)] px-2 text-xs text-[var(--ink)]"
                value={pageSize}
                onChange={(e) => {
                  setPageSize(Number(e.target.value))
                  setPage(1)
                }}
              >
                <option value={25}>25 条/页</option>
                <option value={50}>50 条/页</option>
                <option value={100}>100 条/页</option>
              </select>
              <button
                type="button"
                className="btn h-7 px-2 text-xs"
                disabled={curPage <= 1}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                <ChevronLeft className="h-3.5 w-3.5" />
              </button>
              <span className="mono px-1">
                {curPage} / {totalPages}
              </span>
              <button
                type="button"
                className="btn h-7 px-2 text-xs"
                disabled={curPage >= totalPages}
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              >
                <ChevronRight className="h-3.5 w-3.5" />
              </button>
            </div>
          </div>
        )}
      </section>
    </div>
  )
}
