import { useCallback, useEffect, useState } from 'react'
import { api, type PMAccountView, type PMMarket, type PMOrder, type PMShadowStatus, type PMSignal, type PMSimParams } from './api'

// 预测市场模拟盘：账户列表与全局熔断（预测页首页）、账户详情（#/predict/acct/<id>）、下单面板（事件详情）。
// 成交模型：按实时盘口逐档吃单（IOC），吃不完的取消；手续费按每档成交价计算；结算按市场结果。见 docs/polymarket.md §7。

const money = (x: number) => `${x < 0 ? '−' : ''}$${Math.abs(x).toFixed(2)}`
const cents = (p: number) => `${(p * 100).toFixed(1)}¢`
const signed = (x: number) => <span className={x > 0 ? 'up' : x < 0 ? 'down' : ''}>{x > 0 ? '+' : ''}{money(x)}</span>
const pad = (n: number) => String(n).padStart(2, '0')
function hms(iso: string) {
  const d = new Date(iso)
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}
const STATUS: Record<PMOrder['status'], string> = { filled: '成交', partial: '部分成交', rejected: '拒单' }

/** 预测页首页的模拟盘面板。 */
export function SimPanel() {
  const [accts, setAccts] = useState<PMAccountView[] | null>(null)
  const [halt, setHalt] = useState(false)
  const [name, setName] = useState('')
  const [cash, setCash] = useState('1000')
  const [error, setError] = useState('')
  const load = useCallback(() => api.pmSim().then((r) => { setAccts(r.accounts); setHalt(r.globalHalt) }, (e: Error) => setError(e.message)), [])
  useEffect(() => { load() }, [load])

  const create = () => {
    const c = Number(cash)
    if (!name.trim() || !(c > 0)) return setError('请填写名称和初始资金')
    api.pmSimCreate(name.trim(), c).then(() => { setName(''); setError(''); load() }, (e: Error) => setError(e.message))
  }
  const toggleHalt = () => {
    if (!halt && !confirm('打开全局熔断：拒绝所有账户的一切新委托（模拟与以后的实盘）。确定？')) return
    api.pmHalt(!halt).then((r) => setHalt(r.on), (e: Error) => setError(e.message))
  }
  return (
    <section className="panel">
      <div className="panel-head">
        <strong>模拟盘</strong>
        <span className="muted small">按实时盘口逐档吃单，未成交部分取消；手续费按每档成交价计算；按市场结果结算</span>
        <span style={{ flex: 1 }} />
        <button className={halt ? 'btn btn-primary' : 'btn'} onClick={toggleHalt} title="全局熔断开关">{halt ? '全局熔断中 · 点击解除' : '全局熔断'}</button>
      </div>
      {error && <div className="error" role="alert" style={{ margin: '0 16px 8px' }}>{error}</div>}
      <table className="table">
        <thead><tr><th>账户</th><th className="num">权益</th><th className="num">现金</th><th className="num">浮动盈亏</th><th className="num">已实现</th><th className="num">手续费</th><th className="num">成交笔数</th><th>状态</th></tr></thead>
        <tbody>
          {accts?.map((v) => (
            <tr key={v.account.id} onClick={() => { location.hash = `#/predict/acct/${v.account.id}` }}>
              <td>{v.account.name}</td>
              <td className="num mono">{money(v.equity)}</td>
              <td className="num mono">{money(v.state.cash)}</td>
              <td className="num mono">{signed(v.unrealized)}</td>
              <td className="num mono">{signed(v.state.realized)}</td>
              <td className="num mono">{money(v.state.fees)}</td>
              <td className="num mono">{v.state.orders}</td>
              <td>{v.account.params.halted ? <span className="tag tag-bad">熔断</span> : <span className="tag tag-ok">正常</span>}</td>
            </tr>
          ))}
          {accts?.length === 0 && <tr><td colSpan={8} className="muted">还没有模拟账户。</td></tr>}
        </tbody>
      </table>
      <div className="stock-panel">
        <span className="muted">新建账户</span>
        <input placeholder="名称" value={name} onChange={(e) => setName(e.target.value)} style={{ width: 160 }} />
        <input placeholder="初始资金（USDC）" value={cash} onChange={(e) => setCash(e.target.value)} style={{ width: 140 }} />
        <button className="btn" onClick={create}>新建</button>
        <span className="muted small">默认风控：单笔 $50、单日 $500、单市场 $100，可在账户页修改</span>
      </div>
    </section>
  )
}

/** 账户详情页。 */
export function AccountPage({ id }: { id: number }) {
  const [v, setV] = useState<PMAccountView | null>(null)
  const [p, setP] = useState<PMSimParams | null>(null)
  const [error, setError] = useState('')
  const [msg, setMsg] = useState('')
  const load = useCallback(() => api.pmSimAccount(id).then((r) => { setV(r); setP(r.account.params) }, (e: Error) => setError(e.message)), [id])
  useEffect(() => {
    load()
    const t = window.setInterval(load, 15000) // 估值随实时盘口变化
    return () => window.clearInterval(t)
  }, [load])
  if (!v || !p) return <div className="page">{error ? <div className="error">{error}</div> : <div className="panel empty">加载中…</div>}</div>

  const save = (next: PMSimParams) =>
    api.pmSimParams(id, next).then(() => { setMsg('已保存'); setError(''); load() }, (e: Error) => setError(e.message))
  const del = () => {
    if (!confirm(`删除账户「${v.account.name}」及其全部委托？不可恢复。`)) return
    api.pmSimDelete(id).then(() => { location.hash = '#/predict' }, (e: Error) => setError(e.message))
  }
  const positions = v.state.positions ?? []
  const open = positions.filter((x) => !x.settled), settled = positions.filter((x) => x.settled)
  const num = (k: keyof PMSimParams) => (
    <label className="field-inline">
      <span className="muted small">{{ maxOrderUsd: '单笔上限', maxDailyUsd: '单日上限', maxMarketUsd: '单市场上限', halted: '' }[k]}</span>
      <input style={{ width: 90 }} value={String(p[k])} onChange={(e) => setP({ ...p, [k]: Number(e.target.value) })} />
    </label>
  )
  return (
    <div className="page">
      <div className="page-head">
        <div>
          <a className="link" href="#/predict">← 预测</a>
          <h1>{v.account.name}</h1>
          <p className="sub">
            初始 {money(v.account.cash)} · 权益 <strong>{money(v.equity)}</strong>（{signed(v.equity - v.account.cash)}）· 现金 {money(v.state.cash)}
            · 浮动 {signed(v.unrealized)} · 已实现 {signed(v.state.realized)} · 手续费 {money(v.state.fees)} · 成交额 {money(v.state.volume)}
            {v.globalHalt && <> · <span className="bad-text">全局熔断中</span></>}
          </p>
        </div>
        <button className="btn btn-ghost" onClick={del}>删除账户</button>
      </div>
      {error && <div className="error" role="alert">{error}</div>}
      <section className="panel">
        <div className="panel-head"><strong>风控</strong><span className="muted small">核心强制执行；买入金额按成交额（不含手续费）计</span></div>
        <div className="stock-panel">
          {num('maxOrderUsd')}{num('maxDailyUsd')}{num('maxMarketUsd')}
          <button className="btn" onClick={() => save(p)}>保存</button>
          <button className={p.halted ? 'btn btn-primary' : 'btn'} onClick={() => save({ ...p, halted: !p.halted })}>{p.halted ? '账户熔断中 · 点击解除' : '账户熔断'}</button>
          {msg && <span className="ok-text small">{msg}</span>}
        </div>
      </section>
      <section className="panel">
        <div className="panel-head"><strong>持仓</strong><span className="muted small">未结算的按买一估值（能立即卖出的价格），15 秒刷新</span></div>
        <div className="table-scroll">
          <table className="table">
            <thead><tr><th>市场</th><th>结果</th><th className="num">股数</th><th className="num">成本</th><th className="num">均价</th><th className="num">买一</th><th className="num">估值</th><th className="num">盈亏</th><th>结束</th></tr></thead>
            <tbody>
              {open.map((x) => {
                const val = x.shares * (x.mark ?? 0)
                return (
                  <tr key={x.token} onClick={() => { location.hash = `#/predict/${x.slug}` }}>
                    <td>{x.question}</td><td>{x.outcomeName}</td><td className="num mono">{x.shares.toFixed(2)}</td>
                    <td className="num mono">{money(x.cost)}</td><td className="num mono">{cents(x.cost / x.shares)}</td>
                    <td className="num mono">{x.mark ? cents(x.mark) : '—'}</td><td className="num mono">{money(val)}</td>
                    <td className="num mono">{signed(val - x.cost)}</td><td className="mono small">{hms(x.end)}</td>
                  </tr>
                )
              })}
              {settled.map((x) => (
                <tr key={x.token} onClick={() => { location.hash = `#/predict/${x.slug}` }}>
                  <td>{x.question}</td><td>{x.outcomeName}</td><td className="num mono">{x.shares.toFixed(2)}</td>
                  <td className="num mono">{money(x.cost)}</td><td className="num mono">{cents(x.cost / x.shares)}</td>
                  <td className="num muted">已结算</td><td className="num mono">{money(x.payout ?? 0)}</td>
                  <td className="num mono">{signed((x.payout ?? 0) - x.cost)}</td><td className="mono small">{hms(x.end)}</td>
                </tr>
              ))}
              {positions.length === 0 && <tr><td colSpan={9} className="muted">没有持仓。在事件详情页下单。</td></tr>}
            </tbody>
          </table>
        </div>
      </section>
      <section className="panel">
        <div className="panel-head"><strong>委托</strong><span className="muted small">最近 300 笔</span></div>
        <div className="table-scroll scroll-y">
          <table className="table static">
            <thead><tr><th>时间</th><th>市场</th><th>方向</th><th className="num">请求</th><th>状态</th><th className="num">成交股数</th><th className="num">均价</th><th className="num">成交额</th><th className="num">手续费</th><th>来源</th></tr></thead>
            <tbody>
              {(v.orders ?? []).map((o) => (
                <tr key={o.id}>
                  <td className="mono small nowrap">{hms(o.time)}</td>
                  <td><a className="link" href={`#/predict/${o.slug}`}>{o.question}</a></td>
                  <td className={o.side === 'buy' ? 'up' : 'down'}>{o.side === 'buy' ? '买' : '卖'} {o.outcomeName}</td>
                  <td className="num mono small">{o.reqAmount ? money(o.reqAmount) : `${o.reqShares} 股`}{o.limit ? ` ≤${cents(o.limit)}` : ''}</td>
                  <td>{o.status === 'rejected' ? <span className="bad-text" title={o.reason}>拒单：{o.reason}</span> : STATUS[o.status]}</td>
                  <td className="num mono">{o.fill.shares ? o.fill.shares.toFixed(2) : ''}</td>
                  <td className="num mono">{o.fill.shares ? cents(o.fill.gross / o.fill.shares) : ''}</td>
                  <td className="num mono">{o.fill.shares ? money(o.fill.gross) : ''}</td>
                  <td className="num mono">{o.fill.shares ? money(o.fill.fee) : ''}</td>
                  <td className="muted small">{o.source}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  )
}

/** 事件详情页的下单面板：先试算（按实时盘口），确认后下单。 */
export function OrderTicket({ slug, m, outcome }: { slug: string; m: PMMarket; outcome: number }) {
  const [accts, setAccts] = useState<PMAccountView[]>([])
  const [acct, setAcct] = useState(0)
  const [side, setSide] = useState<'buy' | 'sell'>('buy')
  const [mode, setMode] = useState<'amount' | 'shares'>('amount')
  const [qty, setQty] = useState('10')
  const [limit, setLimit] = useState('')
  const [preview, setPreview] = useState<PMOrder | null>(null)
  const [result, setResult] = useState<PMOrder | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    api.pmSim().then((r) => { setAccts(r.accounts); if (r.accounts.length) setAcct((a) => a || r.accounts[0].account.id) }, () => {})
  }, [result])
  useEffect(() => { setPreview(null); setResult(null) }, [side, mode, qty, limit, outcome, acct, m.conditionId])

  if (m.closed) return null
  if (accts.length === 0) return <p className="disclaimer" style={{ padding: '0 16px 12px' }}>模拟下单：先在预测页新建一个模拟账户。</p>
  const held = accts.find((a) => a.account.id === acct)?.state.positions?.find((p) => p.token === m.tokens[outcome] && !p.settled)?.shares ?? 0
  const req = (dryRun: boolean) => {
    const n = Number(qty)
    // 没填限价时，下单用试算的最差成交价作限价：试算与下单之间盘口变差时不会以更差的价格成交
    const levels = preview?.fill.levels ?? []
    const worst = !dryRun && levels.length ? (side === 'buy' ? Math.max(...levels.map((x) => x.price)) : Math.min(...levels.map((x) => x.price))) : 0
    const l = limit ? Number(limit) / 100 : worst
    if (!(n > 0)) return setError('数量应为正数')
    setError('')
    const amountMode = side === 'buy' && mode === 'amount'
    api.pmSimOrder({ account: acct, slug, conditionId: m.conditionId, outcome, side, dryRun,
      amount: amountMode ? n : undefined, shares: amountMode ? undefined : n, limit: l || undefined })
      .then((o) => (dryRun ? setPreview(o) : (setResult(o), setPreview(null))), (e: Error) => setError(e.message))
  }
  const show = (o: PMOrder, label: string) => (
    <p className="small" style={{ margin: '6px 0 0' }}>
      {label}：{o.status === 'rejected' ? <span className="bad-text">拒单 · {o.reason}</span> : <>
        {STATUS[o.status]} {o.fill.shares.toFixed(2)} 股，均价 {cents(o.fill.gross / o.fill.shares)}，成交额 {money(o.fill.gross)}，手续费 {money(o.fill.fee)}
        <span className="muted">（{(o.fill.levels ?? []).map((l) => `${cents(l.price)}×${l.size}`).join('、')}）</span></>}
    </p>
  )
  return (
    <div style={{ padding: '4px 16px 14px', borderTop: '1px solid var(--line)' }}>
      <div className="stock-panel" style={{ padding: '8px 0 0' }}>
        <strong>模拟下单 · {m.outcomes[outcome]}</strong>
        <select value={acct} onChange={(e) => setAcct(Number(e.target.value))}>
          {accts.map((a) => <option key={a.account.id} value={a.account.id}>{a.account.name}（现金 {money(a.state.cash)}）</option>)}
        </select>
        <div className="seg">
          <button className={side === 'buy' ? 'on' : ''} onClick={() => setSide('buy')}>买</button>
          <button className={side === 'sell' ? 'on' : ''} onClick={() => { setSide('sell'); setMode('shares') }}>卖{held ? `（持 ${held.toFixed(2)}）` : ''}</button>
        </div>
        {side === 'buy' && (
          <div className="seg">
            <button className={mode === 'amount' ? 'on' : ''} onClick={() => setMode('amount')}>按金额</button>
            <button className={mode === 'shares' ? 'on' : ''} onClick={() => setMode('shares')}>按股数</button>
          </div>
        )}
        <input style={{ width: 90 }} value={qty} onChange={(e) => setQty(e.target.value)} placeholder={mode === 'amount' && side === 'buy' ? '美元' : '股'} />
        <span className="muted small">{mode === 'amount' && side === 'buy' ? '美元' : '股'}</span>
        <input style={{ width: 80 }} value={limit} onChange={(e) => setLimit(e.target.value)} placeholder="限价 ¢" title={side === 'buy' ? '最高买价（美分），空为不限' : '最低卖价（美分），空为不限'} />
        <button className="btn" onClick={() => req(true)}>试算</button>
        <button className="btn btn-primary" disabled={!preview || preview.status === 'rejected'} onClick={() => req(false)} title="先试算再下单；没填限价时以试算的最差成交价为限价">下单</button>
      </div>
      {error && <p className="bad-text small">{error}</p>}
      {preview && show(preview, '试算（实时盘口）')}
      {result && show(result, `已提交 #${result.id}`)}
    </div>
  )
}

/** 策略影子模式面板：进行中市场的实时估值与最近信号（只记录、不下单）。 */
export function ShadowPanel({ s }: { s: PMShadowStatus }) {
  const [sigs, setSigs] = useState<PMSignal[]>([])
  useEffect(() => { api.pmSignals(Date.now() - 24 * 3600_000).then(setSigs, () => setSigs([])) }, [s.signals])
  const evals = (s.evals ?? []).filter((e) => e.ok || e.reason !== '未开始')
  const settled = sigs.filter((x) => x.winner >= 0)
  const pnl = (x: PMSignal) => (x.winner === x.outcome ? 1 : 0) - x.ask - x.feeRate * x.ask * (1 - x.ask)
  const edge = (x: number) => <span className={x >= s.minEdge ? 'warn-text' : x > 0 ? '' : 'muted'}>{(x * 100).toFixed(1)}¢</span>
  return (
    <section className="panel">
      <div className="panel-head">
        <strong>策略 · 影子模式</strong>
        <span className="muted small">
          {s.strategy}：按币安现价（换算成 Chainlink 口径）估计 P(Up)，与盘口卖一 + 手续费比较，预期收益 ≥ {(s.minEdge * 100).toFixed(0)}¢ / 股时记录信号，不下单
          · 基差 {Object.entries(s.basis).map(([a, b]) => `${a} ${(b * 1e4).toFixed(1)}bp（${s.basisN[a]} 个点）`).join('、') || '未校准'}
          · 本次启动记录 {s.signals} 条
          {s.lastErr && <> · <span className="bad-text">{s.lastErr}</span></>}
        </span>
      </div>
      <div className="table-scroll">
        <table className="table static">
          <thead><tr><th>市场</th><th className="num">剩余</th><th className="num">起始价</th><th className="num">现价</th><th className="num">P(Up)</th><th className="num">Up 买一 / 卖一</th><th className="num">Down 卖一</th><th className="num">买 Up 预期</th><th className="num">买 Down 预期</th></tr></thead>
          <tbody>
            {evals.map((e) => (
              <tr key={e.target.conditionId}>
                <td><a className="link" href={`#/predict/${e.target.slug}`}>{e.target.series.replace('-up-or-down-', ' ')}</a></td>
                {e.ok ? <>
                  <td className="num mono">{e.tau}s</td>
                  <td className="num mono" title={e.kSrc === 'exact' ? '同一时刻已结算的起始价（精确）' : '开始前 60 秒币安均价按基差换算（估计）'}>{e.k.toFixed(2)}{e.kSrc === 'exact' ? '' : '*'}</td>
                  <td className="num mono">{e.spot.toFixed(2)}</td>
                  <td className="num mono"><strong>{(e.p * 100).toFixed(1)}%</strong></td>
                  <td className="num mono">{cents(e.bidUp)} / {cents(e.askUp)}</td>
                  <td className="num mono">{cents(e.askDn)}</td>
                  <td className="num mono">{edge(e.edgeUp)}</td>
                  <td className="num mono">{edge(e.edgeDn)}</td>
                </> : <td colSpan={8} className="muted">{e.reason}</td>}
              </tr>
            ))}
            {evals.length === 0 && <tr><td colSpan={9} className="muted">没有进行中的市场。</td></tr>}
          </tbody>
        </table>
      </div>
      <p className="disclaimer" style={{ padding: '0 16px' }}>起始价带 * 的是估计值。预期收益 = 模型概率 − 卖一 − 每股手续费，未计深度与延迟。</p>
      <div className="panel-head"><strong>最近 24 小时信号</strong>
        <span className="muted small">{sigs.length} 条，已出结果 {settled.length} 条。按信号时的卖一买 1 股的结果仅作记录：样本少、未计深度与延迟，不作为策略结论。</span>
      </div>
      <div className="table-scroll scroll-y">
        <table className="table static">
          <thead><tr><th>时间</th><th>市场</th><th>结果</th><th className="num">剩余</th><th className="num">模型概率</th><th className="num">卖一（量）</th><th className="num">预期</th><th>起始价</th><th>结算</th><th className="num">每股结果</th></tr></thead>
          <tbody>
            {sigs.slice(0, 100).map((x) => (
              <tr key={x.id}>
                <td className="mono small nowrap">{hms(new Date(x.t).toISOString())}</td>
                <td><a className="link" href={`#/predict/${x.slug}`}>{x.slug}</a></td>
                <td>{x.outcome === 0 ? 'Up' : 'Down'}</td>
                <td className="num mono">{x.tau}s</td>
                <td className="num mono">{(x.p * 100).toFixed(1)}%</td>
                <td className="num mono">{cents(x.ask)}（{x.askSize.toFixed(0)}）</td>
                <td className="num mono">{(x.edge * 100).toFixed(1)}¢</td>
                <td className="small">{x.kSrc === 'exact' ? '精确' : '估计'}</td>
                <td>{x.winner < 0 ? <span className="muted">未出</span> : x.winner === 0 ? 'Up' : 'Down'}</td>
                <td className="num mono">{x.winner < 0 ? '' : signed(pnl(x))}</td>
              </tr>
            ))}
            {sigs.length === 0 && <tr><td colSpan={10} className="muted">还没有信号。</td></tr>}
          </tbody>
        </table>
      </div>
    </section>
  )
}
