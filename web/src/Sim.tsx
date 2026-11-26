import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, ApiError, type PortfolioData, type SecurityHit, type SimAccount, type SimOrder, type SimParams, type SimPlan, type SimView } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { pct } from './temperature'

const STATUS: Record<string, [string, string]> = {
  pending: ['待成交', 'tag-warn'],
  filled: ['已成交', 'tag-ok'],
  rejected: ['已拒绝', 'tag-bad'],
  expired: ['已过期', 'tag-muted'],
  canceled: ['已撤单', 'tag-muted'],
}
const SOURCE: Record<string, string> = { manual: '手工', rebalance: '组合调仓', strategy: '策略调仓' }
const money = (x: number) => x.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
const qtyText = (x: number) => x.toLocaleString('zh-CN', { maximumFractionDigits: 2 })

function dir(x: number) {
  if (Math.abs(x) < 0.005) return ''
  return x > 0 ? 'up' : 'down'
}

export default function Sim() {
  const [accounts, setAccounts] = useState<SimAccount[]>([])
  const [defaults, setDefaults] = useState<SimParams | null>(null)
  const [selected, setSelected] = useState(0)
  const [view, setView] = useState<SimView | null>(null)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  const loadList = useCallback(() => api.simAccounts().then((r) => {
    setAccounts(r.accounts)
    setDefaults(r.defaults)
    setSelected((cur) => (r.accounts.some((a) => a.id === cur) ? cur : r.accounts[0]?.id ?? 0))
  }, (e) => setError(e.message)), [])
  useEffect(() => { loadList() }, [loadList])

  const loadView = useCallback(() => {
    if (!selected) { setView(null); return }
    api.simAccount(selected).then(setView, (e) => { setView(null); setError(e.message) })
  }, [selected])
  useEffect(loadView, [loadView])

  const remove = async () => {
    if (!view || !confirm(`删除模拟账户“${view.account.name}”及其全部委托记录？`)) return
    await api.deleteSimAccount(view.account.id).catch((e) => setError(e.message))
    loadList()
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>模拟交易</h1>
          <p className="sub">
            与券商无关的模拟账户：委托在提交日之后的第一个交易日按本地收盘价撮合（只用今天之前的日线），整手、T+1、佣金、印花税、滑点均可设置。
            持仓与资金由全部委托回放得到，每笔都可追溯。只用于检验策略，不构成投资建议。
          </p>
        </div>
        <div className="actions">
          <button className="btn btn-primary" disabled={creating} onClick={() => setCreating(true)}>新建账户</button>
        </div>
      </div>
      {error && <div className="error" role="alert" onClick={() => setError('')}>{error}</div>}

      <div className="pf-layout">
        <aside className="panel pf-list">
          {accounts.length === 0 && <div className="empty">还没有模拟账户。</div>}
          {accounts.map((a) => (
            <button key={a.id} className={`pf-item ${a.id === selected ? 'on' : ''}`} onClick={() => { setCreating(false); setSelected(a.id) }}>
              <strong>{a.name}</strong>
              <span className="muted small">初始 {money(a.cash)} · {a.start} 起</span>
            </button>
          ))}
        </aside>
        <div className="pf-main">
          {creating && defaults ? (
            <CreateForm defaults={defaults} onCancel={() => setCreating(false)} onCreated={(id) => { setCreating(false); loadList().then(() => setSelected(id)) }} />
          ) : view ? (
            <Account view={view} reload={loadView} onDelete={remove} onError={setError} />
          ) : null}
        </div>
      </div>
    </div>
  )
}

function CreateForm({ defaults, onCancel, onCreated }: { defaults: SimParams; onCancel: () => void; onCreated: (id: number) => void }) {
  const [name, setName] = useState('')
  const [cash, setCash] = useState(1000000)
  const [start, setStart] = useState('')
  const [p, setP] = useState<SimParams>(defaults)
  const [error, setError] = useState('')
  const num = (k: keyof SimParams, label: string, step: number, scale = 1, help = '') => (
    <label className="field">
      <span>{label}</span>
      <input type="number" step={step} value={+(p[k] * scale).toFixed(6)} onChange={(e) => setP({ ...p, [k]: (e.target.valueAsNumber || 0) / scale })} />
      {help && <small className="muted">{help}</small>}
    </label>
  )
  const create = async () => {
    setError('')
    try {
      const r = await api.createSimAccount({ name, cash, start, params: p })
      onCreated(r.id)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }
  return (
    <section className="panel pf-editor">
      <div className="panel-head"><strong>新建模拟账户</strong><span className="muted">参数创建后不可修改（改参数会让历史成交失去依据）</span></div>
      <div className="form-grid">
        <label className="field"><span>名称</span><input value={name} maxLength={40} onChange={(e) => setName(e.target.value)} /></label>
        <label className="field"><span>初始资金（元）</span><input type="number" step={10000} value={cash} onChange={(e) => setCash(e.target.valueAsNumber || 0)} /></label>
        <label className="field"><span>起始日（可空）</span><input type="date" value={start} onChange={(e) => setStart(e.target.value)} /><small className="muted">空为今天；净值曲线从这天开始</small></label>
        {num('commissionRate', '佣金费率（万分之）', 0.1, 10000)}
        {num('minCommission', '最低佣金（元）', 1)}
        {num('stampDuty', '印花税（万分之，股票卖出）', 0.5, 10000)}
        {num('slippageBps', '滑点（基点）', 1, 1, '买入价上浮、卖出价下浮')}
        {num('lotSize', '每手股数', 100)}
        {num('expireDays', '委托有效期（交易日）', 1, 1, '停牌等原因超过这么多交易日未成交则过期')}
        {num('maxOrderAmount', '单笔金额上限（元，0 不限）', 10000, 1, '风控：超过则拒绝提交')}
      </div>
      {error && <div className="error" style={{ margin: '0 16px 12px' }}>{error}</div>}
      <div className="form-foot" style={{ padding: '0 16px 16px' }}>
        <span style={{ flex: 1 }} />
        <button className="btn" onClick={onCancel}>取消</button>
        <button className="btn btn-primary" disabled={!name.trim()} onClick={create}>创建</button>
      </div>
    </section>
  )
}

function Account({ view, reload, onDelete, onError }: { view: SimView; reload: () => void; onDelete: () => void; onError: (s: string) => void }) {
  const { account: a, state: s } = view
  const ret = s.equity / a.cash - 1
  const curve = useMemo(() => s.curve ?? [], [s.curve])
  const bench = useMemo(() => view.bench ?? [], [view.bench])
  const benchRet = curve.length > 1 && bench[0] > 0 ? bench[bench.length - 1] / bench[0] - 1 : null
  const name = (code: string) => view.names[code] ?? ''

  const option = useMemo<EChartsOption | null>(() => {
    if (curve.length < 2) return null
    const ink = cssVar('--ink-2'), line = cssVar('--line')
    return {
      animation: false,
      tooltip: { trigger: 'axis', valueFormatter: (v: unknown) => (typeof v === 'number' ? v.toFixed(4) : String(v)) },
      legend: { top: 0, left: 0, textStyle: { color: ink }, itemWidth: 14, itemHeight: 2 },
      grid: { left: 56, right: 24, top: 32, bottom: 48 },
      xAxis: { type: 'category', data: curve.map((p) => p.date), boundaryGap: false, axisLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      yAxis: { type: 'value', scale: true, splitLine: { lineStyle: { color: line } }, axisLabel: { color: ink } },
      dataZoom: [{ type: 'inside' }, { type: 'slider', height: 16, bottom: 8, borderColor: line, textStyle: { color: ink } }],
      series: [
        { name: '账户净值', type: 'line', data: curve.map((p) => p.equity / a.cash), showSymbol: false, lineStyle: { width: 1.8, color: cssVar('--accent') }, itemStyle: { color: cssVar('--accent') } },
        { name: '沪深300', type: 'line', data: bench.map((b) => (bench[0] > 0 ? b / bench[0] : null)), showSymbol: false, lineStyle: { width: 1.1, color: ink }, itemStyle: { color: ink } },
      ],
    }
  }, [curve, bench, a.cash])

  const tiles: [string, string, string][] = [
    ['总资产', money(s.equity), `初始 ${money(a.cash)}`],
    ['总收益', pct(ret, 2), benchRet === null ? '' : `同期沪深300 ${pct(benchRet, 2)}`],
    ['现金', money(s.cash), `占 ${pct(s.cash / s.equity).replace('+', '')}`],
    ['已实现盈亏', money(s.realized), `累计费用 ${money(s.fees)}`],
  ]
  return (
    <>
      <section className="panel">
        <div className="panel-head">
          <strong>{a.name}</strong>
          <span className="muted">
            {a.start} 起 · 回放到 {s.asOf || '—'}（只撮合 {view.cutoff} 之前的日线）· 佣金万 {(a.params.commissionRate * 1e4).toFixed(1)}、最低 {a.params.minCommission} 元、
            印花税万 {(a.params.stampDuty * 1e4).toFixed(1)}、滑点 {a.params.slippageBps} 基点
            {a.params.maxOrderAmount > 0 && `、单笔上限 ${money(a.params.maxOrderAmount)}`}
          </span>
          <span style={{ flex: 1 }} />
          <button className="btn btn-ghost" onClick={reload}>刷新</button>
          <button className="btn btn-ghost" onClick={onDelete}>删除</button>
        </div>
      </section>
      <div className="pf-tiles">
        {tiles.map(([k, v, sub]) => (
          <div key={k} className="panel pf-tile"><span className="muted small">{k}</span><strong className="mono">{v}</strong><span className="muted small">{sub}</span></div>
        ))}
      </div>
      {option && (
        <section className="panel chart-panel">
          <div className="panel-head"><strong>净值</strong><span className="muted">账户总资产 ÷ 初始资金，沪深300 为价格指数</span></div>
          <EChart option={option} height={300} />
        </section>
      )}
      <section className="panel">
        <div className="panel-head"><strong>持仓</strong><span className="muted">分红送转按除权乘数折算为持股数（相当于红利再投资）</span></div>
        {s.positions && s.positions.length > 0 ? (
          <table className="table static">
            <thead><tr><th>证券</th><th className="num">数量</th><th className="num">成本</th><th className="num">最新价</th><th className="num">市值</th><th className="num">浮动盈亏</th><th className="num">占比</th></tr></thead>
            <tbody>
              {s.positions.map((p) => (
                <tr key={p.code}>
                  <td><a className="mono" href={`#/quote/${p.kind}/${p.code}`}>{p.code}</a> {name(p.code)}</td>
                  <td className="num mono">{qtyText(p.qty)}</td>
                  <td className="num mono">{money(p.cost)}</td>
                  <td className="num mono">{p.price.toFixed(3)} <span className="muted small">{p.priceDate.slice(5)}</span></td>
                  <td className="num mono">{money(p.value)}</td>
                  <td className={`num mono ${dir(p.pnl)}`}>{money(p.pnl)}</td>
                  <td className="num mono">{((p.value / s.equity) * 100).toFixed(1)}%</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : <div className="empty">空仓。</div>}
      </section>
      <OrderForm accountId={a.id} onPlaced={reload} onError={onError} />
      <RebalanceForm accountId={a.id} onPlaced={reload} />
      <Orders orders={view.orders} name={name} accountId={a.id} onChanged={reload} onError={onError} />
    </>
  )
}

function OrderForm({ accountId, onPlaced, onError }: { accountId: number; onPlaced: () => void; onError: (s: string) => void }) {
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<SecurityHit[]>([])
  const [code, setCode] = useState('')
  const [side, setSide] = useState<'buy' | 'sell'>('buy')
  const [qty, setQty] = useState(100)
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!q.trim()) { setHits([]); return }
    const t = setTimeout(() => api.searchSecurities(q).then((r) => setHits(r.items.filter((h) => h.kind !== 'ofund')), () => {}), 250)
    return () => clearTimeout(t)
  }, [q])
  const submit = async () => {
    setBusy(true)
    try {
      await api.placeSimOrders(accountId, 'manual', [{ code, side, qty, note }])
      setNote('')
      onPlaced()
    } catch (e) {
      onError(e instanceof ApiError ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="panel">
      <div className="panel-head"><strong>下单</strong><span className="muted">按最新收盘价做风控检查；在下一个交易日收盘成交</span></div>
      <div className="sim-order">
        <div className="fund-search sim-search">
          <input placeholder={code ? `已选 ${code}，可重新搜索` : '搜索股票或场内基金'} value={q} onChange={(e) => setQ(e.target.value)} />
          {hits.length > 0 && (
            <ul className="fund-hits">
              {hits.map((h) => (
                <li key={h.kind + h.code}>
                  <span className="mono">{h.code}</span> {h.name || '（无名称）'}
                  {!h.hasData && <span className="tag tag-warn">本地无数据</span>}
                  <button className="btn btn-ghost" onClick={() => { setCode(h.code); setQ(''); setHits([]) }}>选择</button>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="seg">
          <button className={side === 'buy' ? 'on' : ''} onClick={() => setSide('buy')}>买入</button>
          <button className={side === 'sell' ? 'on' : ''} onClick={() => setSide('sell')}>卖出</button>
        </div>
        <input className="pf-weight" type="number" step={100} min={0} value={qty} onChange={(e) => setQty(e.target.valueAsNumber || 0)} />
        <input className="sim-note" placeholder="备注（可空）" value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} />
        <button className="btn btn-primary" disabled={busy || !code || qty <= 0} onClick={submit}>提交委托</button>
      </div>
    </section>
  )
}

function RebalanceForm({ accountId, onPlaced }: { accountId: number; onPlaced: () => void }) {
  const [portfolios, setPortfolios] = useState<PortfolioData[]>([])
  const [mode, setMode] = useState<'portfolio' | 'strategy'>('portfolio')
  const [pid, setPid] = useState(0)
  const [proxy, setProxy] = useState('510300')
  const [plan, setPlan] = useState<SimPlan | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { api.portfolios().then((l) => { setPortfolios(l); setPid((cur) => cur || l[0]?.id || 0) }, () => {}) }, [])

  const preview = async () => {
    setError('')
    setPlan(null)
    try {
      setPlan(await api.planSimRebalance(accountId, mode === 'portfolio' ? { portfolioId: pid } : { strategyCode: proxy }))
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }
  const confirmPlan = async () => {
    if (!plan) return
    setBusy(true)
    setError('')
    try {
      await api.placeSimOrders(accountId, mode === 'portfolio' ? 'rebalance' : 'strategy',
        plan.plan.orders.map((o) => ({ code: o.code, side: o.side, qty: o.qty, note: `${plan.plan.note}：${o.note ?? ''}` })))
      setPlan(null)
      onPlaced()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  const sp = plan?.strategy
  return (
    <section className="panel">
      <div className="panel-head">
        <strong>调仓</strong>
        <span className="muted">按最新收盘价估算，先卖后买、整手取整、预留 0.5% 现金；预览确认后才提交</span>
      </div>
      <div className="sim-order">
        <div className="seg">
          <button className={mode === 'portfolio' ? 'on' : ''} onClick={() => { setMode('portfolio'); setPlan(null) }}>按组合权重</button>
          <button className={mode === 'strategy' ? 'on' : ''} onClick={() => { setMode('strategy'); setPlan(null) }}>按温度计仓位</button>
        </div>
        {mode === 'portfolio' ? (
          <select className="sim-select" value={pid} onChange={(e) => setPid(+e.target.value)}>
            {portfolios.length === 0 && <option value={0}>（先在组合页建组合）</option>}
            {portfolios.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </select>
        ) : (
          <label className="field-inline sim-proxy"><span>代理证券</span><input value={proxy} maxLength={6} onChange={(e) => setProxy(e.target.value.trim())} /></label>
        )}
        <button className="btn" disabled={mode === 'portfolio' ? !pid : !/^\d{6}$/.test(proxy)} onClick={preview}>生成预览</button>
      </div>
      {error && <div className="error" style={{ margin: '0 16px 12px' }}>{error}</div>}
      {sp && (
        <p className="disclaimer" style={{ padding: '0 16px 8px' }}>
          温度计分层仓位（{sp.date}）：估值档 {sp.tier || '—'} 底仓 {(sp.base * 100).toFixed(0)}%
          {sp.overlay !== 0 && `，${sp.lastSignal} ${sp.overlay > 0 ? '+' : ''}${(sp.overlay * 100).toFixed(0)}%`} → 目标 {(sp.weight * 100).toFixed(0)}%。
          当前参数的回测没有跑赢估值档基线，这里只用于模拟检验。
        </p>
      )}
      {plan && (plan.plan.orders.length === 0 ? (
        <div className="empty">已经接近目标，不需要调仓。</div>
      ) : (
        <>
          <table className="table static">
            <thead><tr><th>方向</th><th>证券</th><th className="num">数量</th><th>说明</th></tr></thead>
            <tbody>
              {plan.plan.orders.map((o, i) => (
                <tr key={i}>
                  <td className={o.side === 'buy' ? 'up' : 'down'}>{o.side === 'buy' ? '买入' : '卖出'}</td>
                  <td className="mono">{o.code}</td>
                  <td className="num mono">{qtyText(o.qty)}</td>
                  <td className="muted small">{o.note}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="form-foot" style={{ padding: '8px 16px 16px' }}>
            <span className="muted small">{plan.plan.note}</span>
            <span style={{ flex: 1 }} />
            <button className="btn" onClick={() => setPlan(null)}>取消</button>
            <button className="btn btn-primary" disabled={busy} onClick={confirmPlan}>提交 {plan.plan.orders.length} 笔委托</button>
          </div>
        </>
      ))}
    </section>
  )
}

function Orders({ orders, name, accountId, onChanged, onError }: { orders: SimOrder[]; name: (c: string) => string; accountId: number; onChanged: () => void; onError: (s: string) => void }) {
  const list = [...orders].reverse()
  const cancel = async (o: SimOrder) => {
    try {
      await api.cancelSimOrder(accountId, o.id)
      onChanged()
    } catch (e) {
      onError(e instanceof ApiError ? e.message : String(e))
    }
  }
  return (
    <section className="panel">
      <div className="panel-head"><strong>委托</strong><span className="muted">{orders.length} 笔；委托只新增、不删除</span></div>
      {list.length === 0 ? <div className="empty">还没有委托。</div> : (
        <div className="scroll-y">
          <table className="table static">
            <thead><tr><th>#</th><th>提交</th><th>证券</th><th>方向</th><th className="num">数量</th><th>状态</th><th>成交</th><th className="num">成交价</th><th className="num">金额</th><th className="num">费用</th><th>来源 / 说明</th><th /></tr></thead>
            <tbody>
              {list.map((o) => {
                const [label, cls] = STATUS[o.status] ?? [o.status, 'tag-muted']
                return (
                  <tr key={o.id}>
                    <td className="mono muted">{o.id}</td>
                    <td className="mono nowrap">{o.placedDate}</td>
                    <td className="nowrap"><span className="mono">{o.code}</span> {name(o.code)}</td>
                    <td className={o.side === 'buy' ? 'up' : 'down'}>{o.side === 'buy' ? '买入' : '卖出'}</td>
                    <td className="num mono">{qtyText(o.qty)}</td>
                    <td><span className={`tag ${cls}`}>{label}</span>{o.reason && <div className="muted small">{o.reason}</div>}</td>
                    <td className="mono nowrap">{o.fillDate || '—'}</td>
                    <td className="num mono">{o.status === 'filled' ? (o.fillPrice ?? 0).toFixed(3) : '—'}</td>
                    <td className="num mono">{o.status === 'filled' ? money(o.amount ?? 0) : '—'}</td>
                    <td className="num mono">{o.status === 'filled' ? money(o.fee ?? 0) : '—'}</td>
                    <td className="small"><span className="muted">{SOURCE[o.source] ?? o.source}</span>{o.note && <div className="muted">{o.note}</div>}</td>
                    <td>{o.status === 'pending' && <button className="btn btn-ghost" onClick={() => cancel(o)}>撤单</button>}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
