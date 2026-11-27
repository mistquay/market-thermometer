import { useCallback, useEffect, useMemo, useState } from 'react'
import { ArrowLeft, Plus, Radio, ShieldAlert, Trash2 } from 'lucide-react'
import { api, type PMAccountView, type PMMarket, type PMOrder, type PMShadowStatus, type PMSignal, type PMSimParams } from './api'
import EChart, { cssVar, type EChartsOption } from './EChart'
import { useTheme } from './theme'
import { useConfirm, useToast } from './ui/Feedback'

const money = (x: number) => `${x < 0 ? '−' : ''}$${Math.abs(x).toFixed(2)}`
const cents = (p: number) => `${(p * 100).toFixed(1)}¢`
const signed = (x: number) => (
  <span className={x > 0 ? 'us-up' : x < 0 ? 'us-down' : ''}>
    {x > 0 ? '+' : ''}
    {money(x)}
  </span>
)

/** 标准正态分布 CDF Φ(z)（Abramowitz & Stegun 7.1.26，误差 < 1.5e-7）。 */
function normalCDF(z: number) {
  const sign = z < 0 ? -1 : 1
  const x = Math.abs(z) / Math.SQRT2
  const t = 1 / (1 + 0.3275911 * x)
  const y = 1 - (((((1.061405429 * t - 1.453152027) * t + 1.421413741) * t - 0.284496736) * t + 0.254829592) * t) * Math.exp(-x * x)
  return 0.5 * (1 + sign * y)
}

/** 单位方差标准化 Student-t CDF：t = z * sqrt(df / (df - 2))，用复合 Simpson 积分精确绘制曲线。 */
function studentTUnitCDF(z: number, df: number) {
  if (!(df > 2)) return normalCDF(z)
  const x = Math.abs(z) * Math.sqrt(df / (df - 2))
  if (x === 0) return 0.5
  // 积分 ∫_0^x (1 + u^2 / df)^(-(df+1)/2) du，再用 lnGamma 计算归一化常数
  const logGamma = (v: number): number => {
    const g = 7
    const c = [
      0.99999999999980993, 676.5203681218851, -1259.1392167224028, 771.32342877765313,
      -176.61502916214059, 12.507343278686905, -0.13857109526572012, 9.9843695780195716e-6, 1.5056327351493116e-7,
    ]
    let a = c[0]
    for (let i = 1; i < g + 2; i++) a += c[i] / (v - 1 + i)
    const t = v - 1 + g + 0.5
    return 0.5 * Math.log(2 * Math.PI) + (v - 0.5) * Math.log(t) - t + Math.log(a)
  }
  const coef = Math.exp(logGamma((df + 1) / 2) - logGamma(df / 2)) / Math.sqrt(df * Math.PI)
  const steps = 40
  const h = x / steps
  const f = (u: number) => Math.pow(1 + (u * u) / df, -(df + 1) / 2)
  let sum = f(0) + f(x)
  for (let i = 1; i < steps; i++) sum += (i % 2 === 0 ? 2 : 4) * f(i * h)
  const area = coef * (h / 3) * sum
  const p = Math.min(1, Math.max(0, 0.5 + (z >= 0 ? area : -area)))
  return p
}
const pad = (n: number) => String(n).padStart(2, '0')
function hms(iso: string) {
  const d = new Date(iso)
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}
const STATUS: Record<PMOrder['status'], string> = { filled: '成交', partial: '部分成交', rejected: '拒单' }

const inputCls =
  'h-8.5 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-3 text-sm text-[var(--ink)] placeholder:text-[var(--ink-3)] focus:border-[var(--accent)] focus:ring-2 focus:ring-[var(--accent-soft)] focus:outline-none'

/** 预测页首页的模拟盘面板。 */
export function SimPanel() {
  const [accts, setAccts] = useState<PMAccountView[] | null>(null)
  const [halt, setHalt] = useState(false)
  const [name, setName] = useState('')
  const [cash, setCash] = useState('1000')
  const [error, setError] = useState('')
  const confirm = useConfirm()
  const toast = useToast()
  const load = useCallback(
    () =>
      api.pmSim().then(
        (r) => {
          setAccts(r.accounts)
          setHalt(r.globalHalt)
        },
        (e: Error) => setError(e.message),
      ),
    [],
  )
  useEffect(() => {
    load()
  }, [load])

  const create = () => {
    const c = Number(cash)
    if (!name.trim() || !(c > 0)) return setError('请填写名称和初始资金')
    api.pmSimCreate(name.trim(), c).then(
      () => {
        setName('')
        setError('')
        load()
      },
      (e: Error) => setError(e.message),
    )
  }
  const toggleHalt = async () => {
    if (!halt) {
      const ok = await confirm({
        title: '开启全局熔断',
        message: '打开全局熔断后，将拒绝所有账户的一切新委托（模拟与以后的实盘）。确定开启？',
        confirmText: '确认熔断',
        danger: true,
      })
      if (!ok) return
    }
    api.pmHalt(!halt).then((r) => {
      setHalt(r.on)
      toast[r.on ? 'warn' : 'success'](r.on ? '已开启全局熔断' : '已解除全局熔断')
    }, (e: Error) => setError(e.message))
  }

  return (
    <section className="panel overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3.5">
        <div className="flex flex-wrap items-center gap-2.5">
          <strong className="text-sm font-semibold">二元结果模拟盘</strong>
          <span className="text-xs text-[var(--ink-2)]">按实时盘口逐档吃单（IOC），按市场结果结算</span>
        </div>
        <button
          className={
            halt
              ? 'btn border-[var(--bad)] bg-[var(--bad)] text-white hover:opacity-90'
              : 'btn text-[var(--warn)]'
          }
          onClick={toggleHalt}
          title="全局熔断开关"
        >
          <ShieldAlert className="h-3.5 w-3.5" />
          {halt ? '全局熔断中 · 点击解除' : '全局熔断'}
        </button>
      </div>

      {error && (
        <div className="p-4 pb-0">
          <div className="error" role="alert">
            {error}
          </div>
        </div>
      )}

      <div className="table-scroll">
        <table className="table">
          <thead>
            <tr>
              <th>账户</th>
              <th className="num">权益</th>
              <th className="num">现金</th>
              <th className="num">浮动盈亏</th>
              <th className="num">已实现</th>
              <th className="num">手续费</th>
              <th className="num">成交笔数</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            {accts?.map((v) => (
              <tr
                key={v.account.id}
                onClick={() => {
                  location.hash = `#/predict/acct/${v.account.id}`
                }}
              >
                <td className="font-medium text-[var(--accent)]">{v.account.name}</td>
                <td className="num mono font-semibold">{money(v.equity)}</td>
                <td className="num mono">{money(v.state.cash)}</td>
                <td className="num mono">{signed(v.unrealized)}</td>
                <td className="num mono">{signed(v.state.realized)}</td>
                <td className="num mono text-[var(--ink-2)]">{money(v.state.fees)}</td>
                <td className="num mono">{v.state.orders}</td>
                <td>{v.account.params.halted ? <span className="tag tag-bad">熔断</span> : <span className="tag tag-ok">正常</span>}</td>
              </tr>
            ))}
            {accts?.length === 0 && (
              <tr>
                <td colSpan={8} className="muted">
                  还没有模拟账户。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex flex-wrap items-center gap-2.5 border-t border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-3">
        <span className="text-xs font-medium text-[var(--ink-2)]">新建账户</span>
        <input className={`${inputCls} w-40`} placeholder="账户名称" value={name} onChange={(e) => setName(e.target.value)} />
        <input className={`${inputCls} w-36 mono`} placeholder="初始资金 (USDC)" value={cash} onChange={(e) => setCash(e.target.value)} />
        <button className="btn btn-primary" onClick={create}>
          <Plus className="h-3.5 w-3.5" />
          新建
        </button>
        <span className="text-xs text-[var(--ink-3)]">默认风控：单笔 $50、单日 $500、单市场 $100</span>
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
  const confirm = useConfirm()
  const toast = useToast()
  const load = useCallback(
    () =>
      api.pmSimAccount(id).then(
        (r) => {
          setV(r)
          setP(r.account.params)
        },
        (e: Error) => setError(e.message),
      ),
    [id],
  )
  useEffect(() => {
    load()
    const t = window.setInterval(load, 15000)
    return () => window.clearInterval(t)
  }, [load])

  if (!v || !p) {
    return <div className="page">{error ? <div className="error">{error}</div> : <div className="panel empty">加载中…</div>}</div>
  }

  const save = (next: PMSimParams) =>
    api.pmSimParams(id, next).then(
      () => {
        setMsg('已保存')
        setError('')
        load()
      },
      (e: Error) => setError(e.message),
    )
  const del = async () => {
    const ok = await confirm({
      title: '删除模拟账户',
      message: `删除账户「${v.account.name}」及其全部委托？此操作不可恢复。`,
      confirmText: '删除账户',
      danger: true,
    })
    if (!ok) return
    api.pmSimDelete(id).then(() => {
      toast.success(`已删除账户「${v.account.name}」`)
      location.hash = '#/predict'
    }, (e: Error) => setError(e.message))
  }
  const positions = v.state.positions ?? []
  const open = positions.filter((x) => !x.settled)
  const settled = positions.filter((x) => x.settled)
  const num = (k: keyof PMSimParams) => (
    <label className="flex items-center gap-2 text-xs text-[var(--ink-2)]">
      <span>{{ maxOrderUsd: '单笔上限', maxDailyUsd: '单日上限', maxMarketUsd: '单市场上限', maxAssetUsd: '单资产上限', halted: '' }[k]}</span>
      <input
        className={`${inputCls} w-24 mono`}
        value={String(p[k] ?? 0)}
        onChange={(e) => setP({ ...p, [k]: Number(e.target.value) })}
      />
    </label>
  )

  return (
    <div className="page">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <a
            href="#/predict"
            className="mb-1.5 inline-flex items-center gap-1 text-xs font-medium text-[var(--accent)] no-underline hover:underline"
          >
            <ArrowLeft className="h-3.5 w-3.5" />
            返回预测市场
          </a>
          <div className="flex items-center gap-2.5">
            <h1 className="m-0">{v.account.name}</h1>
            {v.globalHalt && <span className="tag tag-bad">全局熔断中</span>}
            {p.halted && <span className="tag tag-bad">账户熔断中</span>}
          </div>
        </div>
        <button className="btn btn-ghost text-[var(--bad)]" onClick={del}>
          <Trash2 className="h-3.5 w-3.5" />
          删除账户
        </button>
      </div>

      {/* 账户 KPI 条 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <div className="panel p-4">
          <div className="text-xs text-[var(--ink-2)]">账户权益</div>
          <div className="mt-1 text-xl font-bold mono">{money(v.equity)}</div>
          <div className="mt-0.5 text-xs mono">{signed(v.equity - v.account.cash)}</div>
        </div>
        <div className="panel p-4">
          <div className="text-xs text-[var(--ink-2)]">可用现金</div>
          <div className="mt-1 text-xl font-bold mono">{money(v.state.cash)}</div>
          <div className="mt-0.5 text-xs text-[var(--ink-3)] mono">初始 {money(v.account.cash)}</div>
        </div>
        <div className="panel p-4">
          <div className="text-xs text-[var(--ink-2)]">浮动盈亏</div>
          <div className="mt-1 text-xl font-bold mono">{signed(v.unrealized)}</div>
          <div className="mt-0.5 text-xs text-[var(--ink-3)]">按买一估值</div>
        </div>
        <div className="panel p-4">
          <div className="text-xs text-[var(--ink-2)]">已实现盈亏</div>
          <div className="mt-1 text-xl font-bold mono">{signed(v.state.realized)}</div>
          <div className="mt-0.5 text-xs text-[var(--ink-3)] mono">成交额 {money(v.state.volume)}</div>
        </div>
        <div className="panel p-4">
          <div className="text-xs text-[var(--ink-2)]">累计手续费</div>
          <div className="mt-1 text-xl font-bold mono">{money(v.state.fees)}</div>
          <div className="mt-0.5 text-xs text-[var(--ink-3)] mono">{v.state.orders} 笔成交</div>
        </div>
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <strong>风控参数</strong>
          <span className="muted small">核心强制执行；买入金额按成交额（不含手续费）计</span>
        </div>
        <div className="flex flex-wrap items-center gap-3 px-5 py-3.5">
          {num('maxOrderUsd')}
          {num('maxDailyUsd')}
          {num('maxMarketUsd')}
          {num('maxAssetUsd')}
          <button className="btn btn-primary" onClick={() => save(p)}>
            保存
          </button>
          <button
            className={p.halted ? 'btn border-[var(--bad)] bg-[var(--bad)] text-white' : 'btn text-[var(--warn)]'}
            onClick={() => save({ ...p, halted: !p.halted })}
          >
            {p.halted ? '账户熔断中 · 点击解除' : '账户熔断'}
          </button>
          {msg && <span className="tag tag-ok">{msg}</span>}
        </div>
      </section>

      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <strong>持仓</strong>
          <span className="muted small">未结算按买一估值，每 15 秒自动刷新</span>
        </div>
        <div className="table-scroll">
          <table className="table">
            <thead>
              <tr>
                <th>市场</th>
                <th>结果</th>
                <th className="num">股数</th>
                <th className="num">成本</th>
                <th className="num">均价</th>
                <th className="num">买一</th>
                <th className="num">估值</th>
                <th className="num">盈亏</th>
                <th>结束</th>
              </tr>
            </thead>
            <tbody>
              {open.map((x) => {
                const val = x.shares * (x.mark ?? 0)
                return (
                  <tr key={x.token} onClick={() => { location.hash = `#/predict/${x.slug}` }}>
                    <td className="font-medium">{x.question}</td>
                    <td><span className="tag tag-cold">{x.outcomeName}</span></td>
                    <td className="num mono">{x.shares.toFixed(2)}</td>
                    <td className="num mono">{money(x.cost)}</td>
                    <td className="num mono">{cents(x.cost / x.shares)}</td>
                    <td className="num mono">{x.mark ? cents(x.mark) : '—'}</td>
                    <td className="num mono">{money(val)}</td>
                    <td className="num mono">{signed(val - x.cost)}</td>
                    <td className="mono small text-[var(--ink-2)]">{hms(x.end)}</td>
                  </tr>
                )
              })}
              {settled.map((x) => (
                <tr key={x.token} onClick={() => { location.hash = `#/predict/${x.slug}` }}>
                  <td>{x.question}</td>
                  <td><span className="tag tag-muted">{x.outcomeName}</span></td>
                  <td className="num mono">{x.shares.toFixed(2)}</td>
                  <td className="num mono">{money(x.cost)}</td>
                  <td className="num mono">{cents(x.cost / x.shares)}</td>
                  <td className="num muted">已结算</td>
                  <td className="num mono">{money(x.payout ?? 0)}</td>
                  <td className="num mono">{signed((x.payout ?? 0) - x.cost)}</td>
                  <td className="mono small text-[var(--ink-2)]">{hms(x.end)}</td>
                </tr>
              ))}
              {positions.length === 0 && (
                <tr>
                  <td colSpan={9} className="muted">
                    没有持仓。在事件详情页下单。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <strong>委托记录</strong>
          <span className="muted small">最近 300 笔</span>
        </div>
        <div className="table-scroll scroll-y">
          <table className="table static">
            <thead>
              <tr>
                <th>时间</th>
                <th>市场</th>
                <th>方向</th>
                <th className="num">请求</th>
                <th>状态</th>
                <th className="num">成交股数</th>
                <th className="num">均价</th>
                <th className="num">成交额</th>
                <th className="num">手续费</th>
                <th>来源</th>
              </tr>
            </thead>
            <tbody>
              {(v.orders ?? []).map((o) => (
                <tr key={o.id}>
                  <td className="mono small nowrap text-[var(--ink-2)]">{hms(o.time)}</td>
                  <td>
                    <a className="link" href={`#/predict/${o.slug}`}>
                      {o.question}
                    </a>
                  </td>
                  <td className={o.side === 'buy' ? 'us-up font-medium' : 'us-down font-medium'}>
                    {o.side === 'buy' ? '买' : '卖'} {o.outcomeName}
                  </td>
                  <td className="num mono small">
                    {o.reqAmount ? money(o.reqAmount) : `${o.reqShares} 股`}
                    {o.limit ? ` ≤${cents(o.limit)}` : ''}
                  </td>
                  <td>
                    {o.status === 'rejected' ? (
                      <span className="tag tag-bad" title={o.reason}>
                        拒单：{o.reason}
                      </span>
                    ) : (
                      <span className="tag tag-ok">{STATUS[o.status]}</span>
                    )}
                  </td>
                  <td className="num mono">{o.fill.shares ? o.fill.shares.toFixed(2) : ''}</td>
                  <td className="num mono">{o.fill.shares ? cents(o.fill.gross / o.fill.shares) : ''}</td>
                  <td className="num mono">{o.fill.shares ? money(o.fill.gross) : ''}</td>
                  <td className="num mono text-[var(--ink-2)]">{o.fill.shares ? money(o.fill.fee) : ''}</td>
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

/** 事件详情页的下单面板：先试算（按实时盘口），确认后下单；支持点击订单簿档位自动填入限价。 */
export function OrderTicket({
  slug,
  m,
  outcome,
  presetLimit,
}: {
  slug: string
  m: PMMarket
  outcome: number
  presetLimit?: { cents: string; side?: 'buy' | 'sell'; seq: number } | null
}) {
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
    if (!presetLimit) return
    setLimit(presetLimit.cents)
    if (presetLimit.side) {
      setSide(presetLimit.side)
      if (presetLimit.side === 'sell') setMode('shares')
    }
  }, [presetLimit])

  useEffect(() => {
    api.pmSim().then(
      (r) => {
        setAccts(r.accounts)
        if (r.accounts.length) setAcct((a) => a || r.accounts[0].account.id)
      },
      () => {},
    )
  }, [result])

  useEffect(() => {
    setPreview(null)
    setResult(null)
  }, [side, mode, qty, limit, outcome, acct, m.conditionId])

  if (m.closed) return null
  if (accts.length === 0) {
    return (
      <p className="disclaimer border-t border-[var(--line)] px-5 py-3">
        模拟下单：先在预测市场首页新建一个模拟账户。
      </p>
    )
  }

  const held =
    accts.find((a) => a.account.id === acct)?.state.positions?.find((p) => p.token === m.tokens[outcome] && !p.settled)?.shares ?? 0

  const req = (dryRun: boolean) => {
    const n = Number(qty)
    const levels = preview?.fill.levels ?? []
    const worst =
      !dryRun && levels.length
        ? side === 'buy'
          ? Math.max(...levels.map((x) => x.price))
          : Math.min(...levels.map((x) => x.price))
        : 0
    const l = limit ? Number(limit) / 100 : worst
    if (!(n > 0)) return setError('数量应为正数')
    setError('')
    const amountMode = side === 'buy' && mode === 'amount'
    api
      .pmSimOrder({
        account: acct,
        slug,
        conditionId: m.conditionId,
        outcome,
        side,
        dryRun,
        amount: amountMode ? n : undefined,
        shares: amountMode ? undefined : n,
        limit: l || undefined,
      })
      .then((o) => (dryRun ? setPreview(o) : (setResult(o), setPreview(null))), (e: Error) => setError(e.message))
  }

  const show = (o: PMOrder, label: string) => (
    <div className="mt-2.5 rounded-lg border border-[var(--line)] bg-[var(--panel)] px-3.5 py-2 text-xs">
      <strong className="text-[var(--ink)]">{label}：</strong>
      {o.status === 'rejected' ? (
        <span className="bad-text">拒单 · {o.reason}</span>
      ) : (
        <span className="mono">
          {STATUS[o.status]} {o.fill.shares.toFixed(2)} 股，均价 {cents(o.fill.gross / o.fill.shares)}，成交额{' '}
          {money(o.fill.gross)}，手续费 {money(o.fill.fee)}
          <span className="muted">（{(o.fill.levels ?? []).map((l) => `${cents(l.price)}×${l.size}`).join('、')}）</span>
        </span>
      )}
    </div>
  )

  return (
    <div className="border-t border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-4">
      <div className="flex flex-wrap items-center gap-2.5">
        <strong className="text-xs font-semibold">模拟下单 · {m.outcomes[outcome]}</strong>
        <select className={inputCls} value={acct} onChange={(e) => setAcct(Number(e.target.value))}>
          {accts.map((a) => (
            <option key={a.account.id} value={a.account.id}>
              {a.account.name}（现金 {money(a.state.cash)}）
            </option>
          ))}
        </select>
        <div className="seg">
          <button className={side === 'buy' ? 'on' : ''} onClick={() => setSide('buy')}>
            买
          </button>
          <button
            className={side === 'sell' ? 'on' : ''}
            onClick={() => {
              setSide('sell')
              setMode('shares')
            }}
          >
            卖{held ? `（持 ${held.toFixed(2)}）` : ''}
          </button>
        </div>
        {side === 'buy' && (
          <div className="seg">
            <button className={mode === 'amount' ? 'on' : ''} onClick={() => setMode('amount')}>
              按金额
            </button>
            <button className={mode === 'shares' ? 'on' : ''} onClick={() => setMode('shares')}>
              按股数
            </button>
          </div>
        )}
        <input
          className={`${inputCls} w-24 mono`}
          value={qty}
          onChange={(e) => setQty(e.target.value)}
          placeholder={mode === 'amount' && side === 'buy' ? '美元' : '股'}
        />
        <span className="muted small">{mode === 'amount' && side === 'buy' ? 'USDC' : '股'}</span>
        <input
          className={`${inputCls} w-22 mono`}
          value={limit}
          onChange={(e) => setLimit(e.target.value)}
          placeholder="限价 ¢"
          title={side === 'buy' ? '最高买价（美分），空为不限' : '最低卖价（美分），空为不限'}
        />
        <div className="flex flex-wrap items-center gap-2">
          <button className="btn" onClick={() => req(true)}>
            试算
          </button>
          <button
            className="btn btn-primary"
            disabled={!preview || preview.status === 'rejected'}
            onClick={() => req(false)}
            title="先试算再下单；没填限价时以试算的最差成交价为限价"
          >
            确认下单
          </button>
          {!preview && <span className="text-[11px] text-[var(--ink-3)]">先点试算后解锁下单</span>}
        </div>
      </div>
      {error && <p className="bad-text small mt-2 mb-0">{error}</p>}
      {preview && show(preview, '试算（实时盘口）')}
      {result && show(result, `已提交 #${result.id}`)}
    </div>
  )
}

/** 策略影子模式面板：进行中市场的实时估值、正态 vs Student-t 白盒分布对比图与最近信号。 */
export function ShadowPanel({ s }: { s: PMShadowStatus }) {
  const [sigs, setSigs] = useState<PMSignal[]>([])
  const { theme } = useTheme()
  useEffect(() => {
    api.pmSignals(Date.now() - 24 * 3600_000).then(setSigs, () => setSigs([]))
  }, [s.signals])
  const evals = (s.evals ?? []).filter((e) => e.ok || e.reason !== '未开始')
  const settled = sigs.filter((x) => x.winner >= 0)
  const pnl = (x: PMSignal) => (x.winner === x.outcome ? 1 : 0) - x.ask - x.feeRate * x.ask * (1 - x.ask)
  const edge = (x: number) => (
    <span className={x >= s.minEdge ? 'us-up font-semibold' : x > 0 ? 'text-[var(--ink)]' : 'muted'}>
      {x > 0 ? '+' : ''}
      {(x * 100).toFixed(1)}¢
    </span>
  )

  // 白盒分布对比图：标准正态 Φ(z) vs Student-t(df) + 实时市场点 (z, P) & (z, Ask)
  const distOption = useMemo<EChartsOption>(() => {
    const ink = cssVar('--ink-2')
    const inkMain = cssVar('--ink')
    const line = cssVar('--chart-grid') || cssVar('--line')
    const accent = cssVar('--accent')
    const warn = cssVar('--warn')
    const ok = cssVar('--us-up') || cssVar('--ok')
    const df = s.df && s.df > 2 ? s.df : 6
    const zs: number[] = []
    for (let i = -30; i <= 30; i++) zs.push(+(i * 0.1).toFixed(1))
    const normPts = zs.map((z) => [z, +(normalCDF(z) * 100).toFixed(2)])
    const tPts = zs.map((z) => [z, +(studentTUnitCDF(z, df) * 100).toFixed(2)])

    const live = (s.evals ?? []).filter((e) => e.ok && e.z !== undefined)
    const modelDots = live.map((e) => ({
      name: `${e.target.series.replace('-up-or-down-', ' ').toUpperCase()} 模型P`,
      value: [Math.max(-3, Math.min(3, +(e.z ?? 0).toFixed(2))), +(e.p * 100).toFixed(1)],
    }))
    const askDots = live
      .filter((e) => e.askUp > 0)
      .map((e) => ({
        name: `${e.target.series.replace('-up-or-down-', ' ').toUpperCase()} Up卖一`,
        value: [Math.max(-3, Math.min(3, +(e.z ?? 0).toFixed(2))), +(e.askUp * 100).toFixed(1)],
      }))

    return {
      animation: false,
      grid: { left: 48, right: 20, top: 34, bottom: 38 },
      tooltip: {
        trigger: 'item',
        backgroundColor: cssVar('--tooltip-bg') || cssVar('--panel'),
        borderColor: cssVar('--tooltip-border') || cssVar('--line-strong'),
        borderWidth: 1,
        textStyle: { color: inkMain, fontSize: 12 },
        formatter: (p: unknown) => {
          const d = p as { seriesName: string; name?: string; value: [number, number] }
          return `${d.name || d.seriesName}<br/>z = <b>${d.value[0]}</b> · 概率/价格 = <b>${d.value[1]}%</b>`
        },
      },
      legend: { top: 0, left: 0, textStyle: { color: ink, fontSize: 11.5 }, itemWidth: 14, itemHeight: 4 },
      xAxis: {
        type: 'value',
        name: '标准化偏离 z',
        nameLocation: 'middle',
        nameGap: 22,
        nameTextStyle: { color: ink, fontSize: 11 },
        min: -3,
        max: 3,
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
        axisLabel: { color: ink, fontSize: 11 },
      },
      yAxis: {
        type: 'value',
        min: 0,
        max: 100,
        interval: 25,
        splitLine: { lineStyle: { color: line, type: 'dashed' } },
        axisLabel: { color: ink, fontSize: 11, formatter: (v: number) => `${v}%` },
      },
      series: [
        {
          name: '标准正态 Φ(z)',
          type: 'line',
          showSymbol: false,
          data: normPts,
          lineStyle: { width: 1.5, color: ink, type: 'dashed' },
          itemStyle: { color: ink },
        },
        {
          name: `Student-t (df=${df}) 厚尾${s.df && s.df > 2 ? ' · 生效中' : ' · 参考'}`,
          type: 'line',
          showSymbol: false,
          data: tPts,
          lineStyle: { width: 2.2, color: accent },
          itemStyle: { color: accent },
        },
        ...(modelDots.length > 0
          ? [
              {
                name: '实时市场 模型 P(Up)',
                type: 'scatter' as const,
                symbolSize: 10,
                data: modelDots,
                itemStyle: { color: ok },
              },
              {
                name: '实时市场 Up 卖一',
                type: 'scatter' as const,
                symbol: 'diamond',
                symbolSize: 10,
                data: askDots,
                itemStyle: { color: warn },
              },
            ]
          : []),
      ],
    }
  }, [s.df, s.evals, theme])

  return (
    <section className="panel overflow-hidden">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] px-5 py-3.5">
        <div className="flex flex-wrap items-center gap-2.5">
          <Radio className="h-4 w-4 text-[var(--accent)]" />
          <strong className="text-sm font-semibold">公允概率策略 · 影子模式（白盒实时推导）</strong>
          <span className="tag tag-cold mono">{s.strategy}</span>
          <span className="tag tag-ok">{s.distribution || (s.df && s.df > 2 ? `Student-t(df=${s.df})` : '标准正态 Φ(z)')}</span>
          <span className="tag tag-muted">阈值 ≥ {(s.minEdge * 100).toFixed(1)}¢</span>
          <span className="tag tag-muted mono">本次记录 {s.signals} 条</span>
          {s.ptbLag > 0 && <span className="tag tag-muted mono">起始价延迟 {s.ptbLag.toFixed(0)}s</span>}
          {s.lastErr && <span className="tag tag-bad">{s.lastErr}</span>}
        </div>
        <span className="mono text-xs text-[var(--ink-2)]">
          基差{' '}
          {Object.entries(s.basis)
            .map(([a, b]) => `${a} ${(b * 1e4).toFixed(1)}±${(s.basisErr[a] ?? 0).toFixed(1)}bp (N=${s.basisN[a]})`)
            .join(' · ') || '未校准'}
        </span>
      </div>

      {/* 白盒模型公式与实时参数说明 */}
      <div className="grid grid-cols-1 gap-3 border-b border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-3 text-xs text-[var(--ink-2)] lg:grid-cols-3">
        <div>
          <div className="font-semibold text-[var(--ink)]">① 概率推导公式（无黑盒）</div>
          <div className="mt-1 mono text-[11px] leading-relaxed">
            z = (ln E[TWAP<sub>60s</sub>] − ln K) / √(σ²·τ<sub>eff</sub> + ε<sub>K</sub>² + ε<sub>end</sub>²)
            <br />
            {s.df && s.df > 2
              ? `P(Up) = StudentT(z · √(${s.df}/${(s.df - 2).toFixed(0)}), df=${s.df})（厚尾降权极端过自信）`
              : 'P(Up) = Φ(z)（标准正态分布，可在「系统设置」调大 df 启用厚尾）'}
          </div>
        </div>
        <div>
          <div className="font-semibold text-[var(--ink)]">② 波动率与手续费口径</div>
          <div className="mt-1 text-[11px] leading-relaxed">
            <div>波动率：{s.volMode || 'BlendedSigma (30m + 5m 混合实现波动率)'}</div>
            <div className="mono">
              实时 σ：
              {Object.entries(s.sigma ?? {})
                .map(([sym, v]) => `${sym} ${(v * 1e4).toFixed(2)}bp/√s`)
                .join(' · ') || '等待采集'}
            </div>
            <div>净预期 Edge = P − 卖一 − feeRate × p(1−p)</div>
          </div>
        </div>
        <div>
          <div className="font-semibold text-[var(--ink)]">③ 参数来源与热更新</div>
          <div className="mt-1 text-[11px] leading-relaxed">
            <div>
              当前参数均持久化在数据库 <span className="mono">app_config</span>：
              <span className="mono text-[var(--ink)]"> minEdge={(s.minEdge * 100).toFixed(1)}¢, df={s.df ?? 0}</span>
            </div>
            <div>
              <a href="#/settings" className="link">
                前往「系统设置 → 配置中心」修改（保存即热更新） →
              </a>
            </div>
          </div>
        </div>
      </div>

      {/* 正态 vs Student-t 白盒分布对比图 */}
      <div className="border-b border-[var(--line)] px-4 pt-3 pb-2">
        <div className="mb-1 flex flex-wrap items-center justify-between gap-2 px-1 text-[11.5px] text-[var(--ink-2)]">
          <span>
            <strong className="text-[var(--ink)]">白盒分布可视化</strong>：对比标准正态与 Student-t(df={s.df && s.df > 2 ? s.df : 6}) 在 |z|∈[1,3] 区间对极端过自信的平滑压缩
          </span>
          <span className="mono text-[var(--ink-3)]">绿圆=模型 P(Up) · 橙菱=市场卖一（垂直差 ≈ 扣费前 Edge）</span>
        </div>
        <EChart option={distOption} height={225} />
      </div>

      <div className="table-scroll">
        <table className="table static">
          <thead>
            <tr>
              <th>市场</th>
              <th className="num">剩余</th>
              <th className="num">起始价 K</th>
              <th className="num">现价 S</th>
              <th className="num">z / 合成σ(bp)</th>
              <th className="num">P(Up) (正态→当前)</th>
              <th className="num">Up 买一 / 卖一</th>
              <th className="num">Down 卖一</th>
              <th className="num">买 Up 净Edge (费)</th>
              <th className="num">买 Down 净Edge (费)</th>
            </tr>
          </thead>
          <tbody>
            {evals.map((e) => (
              <tr key={e.target.conditionId}>
                <td>
                  <a className="link font-medium" href={`#/predict/${e.target.slug}`}>
                    {e.target.series.replace('-up-or-down-', ' ')}
                  </a>
                </td>
                {e.ok ? (
                  <>
                    <td className="num mono">{e.tau}s</td>
                    <td
                      className="num mono"
                      title={e.kSrc === 'exact' ? '同一时刻已结算的起始价（精确）' : `开始前 60 秒币安均价按基差换算（估计，ε_K=${(e.kErrBp ?? 0).toFixed(1)}bp）`}
                    >
                      {e.k.toFixed(2)}
                      {e.kSrc === 'exact' ? '' : '*'}
                    </td>
                    <td className="num mono">{e.spot.toFixed(2)}</td>
                    <td className="num mono small" title={`z=${(e.z ?? 0).toFixed(3)}, 合成标准差=${e.sdBp.toFixed(1)}bp, 结束基差误差=${(e.endErrBp ?? 0).toFixed(1)}bp`}>
                      {(e.z ?? 0) >= 0 ? '+' : ''}
                      {(e.z ?? 0).toFixed(2)} / {e.sdBp.toFixed(1)}
                    </td>
                    <td className="num mono">
                      {e.df && e.df > 2 && e.normalP !== undefined ? (
                        <span title={`正态 ${(e.normalP * 100).toFixed(1)}% → Student-t(df=${e.df}) ${(e.p * 100).toFixed(1)}%`}>
                          <span className="text-[11px] text-[var(--ink-3)]">{(e.normalP * 100).toFixed(1)}%→</span>
                          <strong className="text-[var(--accent)]">{(e.p * 100).toFixed(1)}%</strong>
                        </span>
                      ) : (
                        <strong className="text-[var(--accent)]">{(e.p * 100).toFixed(1)}%</strong>
                      )}
                    </td>
                    <td className="num mono">
                      {cents(e.bidUp)} / {cents(e.askUp)}
                    </td>
                    <td className="num mono">{cents(e.askDn)}</td>
                    <td className="num mono">
                      {edge(e.edgeUp)}
                      {e.feeUp ? <span className="ml-1 text-[10px] text-[var(--ink-3)]">({(e.feeUp * 100).toFixed(1)}¢)</span> : null}
                    </td>
                    <td className="num mono">
                      {edge(e.edgeDn)}
                      {e.feeDn ? <span className="ml-1 text-[10px] text-[var(--ink-3)]">({(e.feeDn * 100).toFixed(1)}¢)</span> : null}
                    </td>
                  </>
                ) : (
                  <td colSpan={9} className="muted">
                    {e.reason}
                  </td>
                )}
              </tr>
            ))}
            {evals.length === 0 && (
              <tr>
                <td colSpan={10} className="muted">
                  没有进行中的市场。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="border-t border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-2.5 text-xs text-[var(--ink-3)]">
        起始价带 * 为估计值。预期收益 = 模型概率 − 卖一 − 每股手续费，只记录信号、不下单。
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-[var(--line)] px-5 py-3">
        <strong className="text-xs font-semibold">最近 24 小时信号</strong>
        <span className="text-xs text-[var(--ink-2)]">
          共 {sigs.length} 条 · 已结算 {settled.length} 条（样本少，不作收益结论）
        </span>
      </div>
      <div className="table-scroll scroll-y">
        <table className="table static">
          <thead>
            <tr>
              <th>时间</th>
              <th>市场</th>
              <th>预测方向</th>
              <th className="num">剩余</th>
              <th className="num">模型概率</th>
              <th className="num">卖一（量）</th>
              <th className="num">预期</th>
              <th>起始价</th>
              <th>结算</th>
              <th className="num">每股结果</th>
            </tr>
          </thead>
          <tbody>
            {sigs.slice(0, 100).map((x) => (
              <tr key={x.id}>
                <td className="mono small nowrap text-[var(--ink-2)]">{hms(new Date(x.t).toISOString())}</td>
                <td>
                  <a className="link" href={`#/predict/${x.slug}`}>
                    {x.slug}
                  </a>
                </td>
                <td>
                  <span className={`tag ${x.outcome === 0 ? 'tag-hot' : 'tag-cold'}`}>{x.outcome === 0 ? 'Up' : 'Down'}</span>
                </td>
                <td className="num mono">{x.tau}s</td>
                <td className="num mono">{(x.p * 100).toFixed(1)}%</td>
                <td className="num mono">
                  {cents(x.ask)}（{x.askSize.toFixed(0)}）
                </td>
                <td className="num mono">{(x.edge * 100).toFixed(1)}¢</td>
                <td className="small">{x.kSrc === 'exact' ? '精确' : '估计'}</td>
                <td>{x.winner < 0 ? <span className="muted">未出</span> : x.winner === 0 ? 'Up' : 'Down'}</td>
                <td className="num mono">{x.winner < 0 ? '' : signed(pnl(x))}</td>
              </tr>
            ))}
            {sigs.length === 0 && (
              <tr>
                <td colSpan={10} className="muted">
                  还没有信号。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}
