import { useCallback, useEffect, useState } from 'react'
import { Check, Fish, Plus, RefreshCw, Scale, ShieldCheck, Trash2, X } from 'lucide-react'
import { api, type PMAccountView, type PMArbResp, type PMExecConfig, type PMProposal, type PMTrade, type PMWhale } from './api'
import { useConfirm, useToast } from './ui/Feedback'

const inputCls =
  'h-8 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-2.5 text-xs mono text-[var(--ink)] focus:border-[var(--accent)] focus:outline-none'

const STATUS_TAG: Record<PMProposal['status'], { cls: string; text: string }> = {
  pending: { cls: 'tag-warn', text: '待确认' },
  approved: { cls: 'tag-ok', text: '已批准' },
  rejected: { cls: 'tag-muted', text: '已拒绝' },
  expired: { cls: 'tag-muted', text: '已过期' },
}

const shortAddr = (a: string) => (a.length > 12 ? `${a.slice(0, 6)}…${a.slice(-4)}` : a)

export function ArbAndQueuePanel({ accounts }: { accounts: PMAccountView[] }) {
  const [shares, setShares] = useState(50)
  const [discover, setDiscover] = useState(true)
  const [arb, setArb] = useState<PMArbResp | null>(null)
  const [arbBusy, setArbBusy] = useState(false)
  const [exec, setExec] = useState<PMExecConfig | null>(null)
  const [proposals, setProposals] = useState<PMProposal[]>([])
  const [whales, setWhales] = useState<PMWhale[]>([])
  const [selectedWhale, setSelectedWhale] = useState('')
  const [minUsd, setMinUsd] = useState(100)
  const [trades, setTrades] = useState<PMTrade[]>([])
  const [tradesBusy, setTradesBusy] = useState(false)
  const [newAddr, setNewAddr] = useState('')
  const [newLabel, setNewLabel] = useState('')
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')
  const confirm = useConfirm()
  const toast = useToast()

  const loadExec = useCallback(() => {
    api.pmExec().then(setExec, (e: Error) => setError(e.message))
    api.pmProposals().then(setProposals, () => {})
  }, [])

  const loadWhales = useCallback(() => {
    api.pmWhales().then(setWhales, () => {})
  }, [])

  const loadTrades = useCallback(
    (user = selectedWhale, minU = minUsd) => {
      setTradesBusy(true)
      api
        .pmTrades({ user, minUsd: user ? 0 : minU, limit: 100 })
        .then(setTrades, (e: Error) => setError(e.message))
        .finally(() => setTradesBusy(false))
    },
    [selectedWhale, minUsd],
  )

  useEffect(() => {
    loadExec()
    loadWhales()
    api.pmArb(50, true, true).then(setArb, () => {})
    const t = window.setInterval(() => api.pmProposals().then(setProposals, () => {}), 5000)
    return () => window.clearInterval(t)
  }, [loadExec, loadWhales])

  const scanArb = () => {
    setArbBusy(true)
    setError('')
    api
      .pmArb(shares, discover, false)
      .then(setArb, (e: Error) => setError(e.message))
      .finally(() => setArbBusy(false))
  }

  const addWhale = (addr = newAddr, label = newLabel) => {
    const a = addr.trim()
    if (!a) return
    setError('')
    setMsg('')
    api.pmWhaleSave({ address: a, label: label.trim() }).then(
      () => {
        setNewAddr('')
        setNewLabel('')
        setMsg(`已关注钱包 ${shortAddr(a)}`)
        loadWhales()
      },
      (e: Error) => setError(e.message),
    )
  }

  const removeWhale = (addr: string) => {
    api.pmWhaleDelete(addr).then(() => {
      if (selectedWhale === addr) setSelectedWhale('')
      loadWhales()
    })
  }

  const saveExec = async () => {
    if (!exec) return
    setMsg('')
    setError('')
    if (exec.mode === 'auto_sim') {
      const ok = await confirm({
        title: '开启模拟盘自动下单',
        message: '策略触发符合条件的信号时将直接调用模拟盘 Broker 吃单（受全局熔断与账户风控上限约束）。确认开启？',
        confirmText: '确认开启',
        danger: true,
      })
      if (!ok) return
    }
    api.pmExecSave(exec).then(
      (saved) => {
        setExec(saved)
        setMsg('已保存执行模式')
        toast.success('已保存策略执行模式')
      },
      (e: Error) => setError(e.message),
    )
  }

  const approve = (p: PMProposal) => {
    setError('')
    setMsg('')
    const acctId = p.accountId || exec?.accountId || accounts[0]?.account.id || 0
    if (!acctId) return setError('请先在“模拟盘与风控”中创建一个模拟账户')
    api.pmProposalApprove(p.id, { accountId: acctId, amount: p.suggestedUsd }).then(
      (r) => {
        setMsg(`提案 #${p.id} 已执行：${r.order.status}${r.order.reason ? '（' + r.order.reason + '）' : ''}`)
        loadExec()
      },
      (e: Error) => {
        setError(e.message)
        loadExec()
      },
    )
  }

  const reject = (p: PMProposal) => {
    api.pmProposalReject(p.id).then(loadExec, (e: Error) => setError(e.message))
  }

  const pendingCount = proposals.filter((p) => p.status === 'pending').length

  return (
    <div className="flex flex-col gap-4">
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      {msg && <div className="notice">{msg}</div>}

      {/* 分级执行模式与人工审批（HITL） */}
      {exec && (
        <section className="panel overflow-hidden">
          <div className="panel-head border-b border-[var(--line)]">
            <ShieldCheck className="h-4 w-4 text-[var(--accent)]" />
            <strong>策略分级执行控制（影子 → 人工确认 → 自动模拟）</strong>
            <span className="muted">遵循逐级放权原则，所有执行强制受核心风控与全局熔断约束</span>
          </div>
          <div className="grid grid-cols-2 gap-3 p-4 sm:grid-cols-4 lg:grid-cols-6">
            <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
              <span>执行模式</span>
              <select className={inputCls} value={exec.mode} onChange={(e) => setExec({ ...exec, mode: e.target.value as PMExecConfig['mode'] })}>
                <option value="shadow">影子模式（只记信号）</option>
                <option value="hitl">人工确认队列（HITL）</option>
                <option value="auto_sim">模拟盘自动下单</option>
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
              <span>绑定模拟账户</span>
              <select className={inputCls} value={exec.accountId} onChange={(e) => setExec({ ...exec, accountId: Number(e.target.value) })}>
                <option value={0}>未绑定</option>
                {accounts.map((a) => (
                  <option key={a.account.id} value={a.account.id}>
                    {a.account.name} (${a.equity.toFixed(0)})
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
              <span>仓位模型</span>
              <select className={inputCls} value={exec.sizing} onChange={(e) => setExec({ ...exec, sizing: e.target.value as 'fixed' | 'kelly' })}>
                <option value="fixed">固定金额</option>
                <option value="kelly">分数凯利</option>
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
              <span>{exec.sizing === 'kelly' ? '单笔上限 ($)' : '单笔金额 ($)'}</span>
              <input type="number" className={inputCls} value={exec.orderUsd} min={5} onChange={(e) => setExec({ ...exec, orderUsd: e.target.valueAsNumber || 20 })} />
            </label>
            <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
              <span>最小 Edge (¢)</span>
              <input
                type="number"
                className={inputCls}
                step={0.5}
                value={Math.round(exec.minEdge * 1000) / 10}
                onChange={(e) => setExec({ ...exec, minEdge: (e.target.valueAsNumber || 0) / 100 })}
              />
            </label>
            <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
              <span>起始价要求</span>
              <select className={inputCls} value={exec.kSrc} onChange={(e) => setExec({ ...exec, kSrc: e.target.value })}>
                <option value="exact">仅精确起始价</option>
                <option value="">全部（含估计）</option>
              </select>
            </label>
          </div>
          <div className="flex flex-wrap items-center gap-4 border-t border-[var(--line)] bg-[var(--bg-subtle)] px-4 py-2.5">
            <label className="flex items-center gap-2 text-xs text-[var(--ink)]">
              <input type="checkbox" checked={exec.notify} onChange={(e) => setExec({ ...exec, notify: e.target.checked })} />
              新提案同时发推送告警（需在设置页开启运维告警）
            </label>
            <span style={{ flex: 1 }} />
            <button className="btn btn-primary" onClick={saveExec}>
              保存策略模式
            </button>
          </div>
        </section>
      )}

      {/* HITL 提案审批队列 */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <strong>人工确认提案队列（HITL）</strong>
          {pendingCount > 0 ? <span className="tag tag-warn">{pendingCount} 条待确认</span> : <span className="muted">当前无待确认提案</span>}
          <button className="btn btn-ghost" style={{ marginLeft: 'auto' }} onClick={loadExec}>
            <RefreshCw className="h-3.5 w-3.5" />
            刷新
          </button>
        </div>
        <div className="table-scroll">
          <table className="table static">
            <thead>
              <tr>
                <th>时间</th>
                <th>市场</th>
                <th>方向</th>
                <th className="num">模型 P</th>
                <th className="num">卖一</th>
                <th className="num">Edge</th>
                <th className="num">建议额</th>
                <th>状态</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {proposals.map((p) => {
                const st = STATUS_TAG[p.status]
                const leftSec = Math.max(0, Math.round((p.expiresAt - Date.now()) / 1000))
                return (
                  <tr key={p.id}>
                    <td className="mono small nowrap">{new Date(p.createdAt).toLocaleTimeString('zh-CN', { hour12: false })}</td>
                    <td className="mono small">
                      <a href={`#/predict/${p.slug}`} className="text-[var(--accent)] no-underline hover:underline">
                        {p.slug}
                      </a>
                    </td>
                    <td>
                      <span className={`tag ${p.outcome === 0 ? 'tag-hot' : 'tag-cold'}`}>{p.outcome === 0 ? 'Up' : 'Down'}</span>
                    </td>
                    <td className="num mono small">{(p.p * 100).toFixed(1)}%</td>
                    <td className="num mono small">{(p.ask * 100).toFixed(1)}¢</td>
                    <td className="num mono small up">+{(p.edge * 100).toFixed(1)}¢</td>
                    <td className="num mono small">${p.suggestedUsd.toFixed(0)}</td>
                    <td>
                      <span className={`tag ${st.cls}`}>{st.text}</span>
                      {p.status === 'pending' && <span className="ml-1 mono text-[11px] text-[var(--ink-3)]">{leftSec}s</span>}
                      {p.reason && <div className="text-[11px] text-[var(--ink-3)]">{p.reason}</div>}
                    </td>
                    <td className="num nowrap">
                      {p.status === 'pending' && (
                        <>
                          <button className="btn btn-primary mr-1.5" onClick={() => approve(p)}>
                            <Check className="h-3.5 w-3.5" />
                            批准
                          </button>
                          <button className="btn btn-ghost" onClick={() => reject(p)}>
                            <X className="h-3.5 w-3.5" />
                            拒绝
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                )
              })}
              {proposals.length === 0 && (
                <tr>
                  <td colSpan={9} className="muted">
                    暂无提案。将上方执行模式切换为“人工确认队列（HITL）”后，新信号会自动进入这里等待审批。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      {/* 套利 / 错价扫描器 */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <Scale className="h-4 w-4 text-[var(--accent)]" />
          <strong>套利与错价实时扫描器</strong>
          <span className="muted">扫描当前进行中系列与已关注事件：二元 Up+Down 与互斥多结果 Yes 之和（按真实深度逐档吃单并扣吃单费）</span>
          <div className="flex flex-wrap items-center gap-2" style={{ marginLeft: 'auto' }}>
            <label className="flex items-center gap-1.5 text-xs text-[var(--ink-2)]">
              <input type="checkbox" checked={discover} onChange={(e) => setDiscover(e.target.checked)} />
              全站 NegRisk 发现
            </label>
            <span className="text-xs text-[var(--ink-2)]">试算份数</span>
            <input type="number" className={`${inputCls} w-20`} value={shares} min={5} onChange={(e) => setShares(e.target.valueAsNumber || 50)} />
            <button className="btn btn-primary" disabled={arbBusy} onClick={scanArb}>
              <RefreshCw className={`h-3.5 w-3.5 ${arbBusy ? 'animate-spin' : ''}`} />
              {arbBusy ? '扫描中…' : '立即扫描'}
            </button>
          </div>
        </div>
        {arb ? (
          <>
            <div className="flex flex-wrap items-center gap-3 border-b border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-2 text-xs text-[var(--ink-2)] mono">
              <span>
                已扫描 {arb.scannedEvents} 个活跃事件、{arb.scannedTokens} 个盘口 · 试算 {arb.targetShares} 份 · 时间{' '}
                {new Date(arb.at).toLocaleTimeString('zh-CN', { hour12: false })}
              </span>
              {(arb.positiveCount ?? 0) > 0 && <span className="tag tag-ok">当前 {arb.positiveCount} 个扣费正套利</span>}
              {arb.discover && <span className="tag tag-muted">含全站 Top25 NegRisk</span>}
            </div>
            <div className="table-scroll">
              <table className="table static">
                <thead>
                  <tr>
                    <th>类型</th>
                    <th>事件</th>
                    <th className="num">可配对份数</th>
                    <th className="num">卖一合计</th>
                    <th className="num">含费总成本</th>
                    <th className="num">每份净收益</th>
                    <th className="num">净利 / ROI</th>
                  </tr>
                </thead>
                <tbody>
                  {arb.opportunities.map((o) => (
                    <tr key={`${o.kind}-${o.slug}`}>
                      <td>
                        <span className={`tag ${o.kind === 'negrisk' ? 'tag-cold' : 'tag-muted'}`}>
                          {o.kind === 'negrisk' ? `多结果(${o.legs.length}腿)` : '二元'}
                        </span>
                      </td>
                      <td>
                        <a href={`#/predict/${o.slug}`} className="font-medium text-[var(--accent)] no-underline hover:underline">
                          {o.title}
                        </a>
                      </td>
                      <td className="num mono">{o.shares.toFixed(1)}</td>
                      <td className="num mono">{(o.rawSumAsk * 100).toFixed(2)}¢</td>
                      <td className="num mono">${o.totalCost.toFixed(2)}</td>
                      <td className={`num mono font-semibold ${o.netPerUnit > 0 ? 'up' : 'down'}`}>
                        {o.netPerUnit >= 0 ? '+' : ''}
                        {(o.netPerUnit * 100).toFixed(2)}¢
                      </td>
                      <td className={`num mono ${o.netProfit > 0 ? 'up' : 'down'}`}>
                        {o.netProfit >= 0 ? '+' : '−'}${Math.abs(o.netProfit).toFixed(2)} ({(o.roi * 100).toFixed(2)}%)
                      </td>
                    </tr>
                  ))}
                  {arb.opportunities.length === 0 && (
                    <tr>
                      <td colSpan={7} className="muted">
                        未扫描到具备足够盘口深度的市场。
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
            {arb.history && arb.history.length > 0 && (
              <div className="border-t border-[var(--line)]">
                <div className="bg-[var(--bg-subtle)] px-5 py-2 text-xs font-medium text-[var(--ink-2)]">
                  后台监控历史命中（扣费后净利 ≥ $0.50 且单份 ≥ 0.5¢，最近 {arb.history.length} 条）
                </div>
                <div className="table-scroll">
                  <table className="table static">
                    <thead>
                      <tr>
                        <th>时间</th>
                        <th>类型</th>
                        <th>事件</th>
                        <th className="num">卖一合计</th>
                        <th className="num">单份净收益</th>
                        <th className="num">总净利</th>
                      </tr>
                    </thead>
                    <tbody>
                      {arb.history.slice(0, 15).map((h, i) => (
                        <tr key={`${h.at}-${h.slug}-${i}`}>
                          <td className="mono small nowrap">{new Date(h.at).toLocaleTimeString('zh-CN', { hour12: false })}</td>
                          <td>
                            <span className="tag tag-ok">{h.kind === 'negrisk' ? '多结果' : '二元'}</span>
                          </td>
                          <td>
                            <a href={`#/predict/${h.slug}`} className="text-[var(--accent)] no-underline hover:underline">
                              {h.title}
                            </a>
                          </td>
                          <td className="num mono small">{(h.rawSumAsk * 100).toFixed(2)}¢</td>
                          <td className="num mono small up">+{(h.netPerUnit * 100).toFixed(2)}¢</td>
                          <td className="num mono small up">+${h.netProfit.toFixed(2)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </>
        ) : (
          <div className="empty">点击右上角“立即扫描”，批量拉取最新 CLOB 订单簿检测扣费后真实套利空间（后台每 90 秒自动巡检并告警）。</div>
        )}
      </section>

      {/* 聪明钱 / 鲸鱼追踪与大额成交流（Data API v2） */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <Fish className="h-4 w-4 text-[var(--accent)]" />
          <strong>聪明钱追踪与公开大额成交流（Data API v2）</strong>
          <span className="muted">无需 API Key，实时拉取全站或指定鲸鱼钱包的最新成交，辅助筛选热门与错价市场</span>
          <div className="flex flex-wrap items-center gap-2" style={{ marginLeft: 'auto' }}>
            {!selectedWhale && (
              <select className={inputCls} value={minUsd} onChange={(e) => setMinUsd(Number(e.target.value))}>
                <option value={0}>全部金额</option>
                <option value={100}>≥ $100</option>
                <option value={500}>≥ $500</option>
                <option value={1000}>≥ $1,000</option>
                <option value={5000}>≥ $5,000</option>
              </select>
            )}
            <button className="btn btn-primary" disabled={tradesBusy} onClick={() => loadTrades()}>
              <RefreshCw className={`h-3.5 w-3.5 ${tradesBusy ? 'animate-spin' : ''}`} />
              {tradesBusy ? '拉取中…' : selectedWhale ? '拉取该钱包成交' : '拉取全站大额成交'}
            </button>
          </div>
        </div>

        {/* 关注钱包栏 */}
        <div className="flex flex-wrap items-center gap-2 border-b border-[var(--line)] bg-[var(--bg-subtle)] px-4 py-2.5">
          <button
            className={`btn ${selectedWhale === '' ? 'btn-primary' : 'btn-ghost'}`}
            onClick={() => {
              setSelectedWhale('')
              loadTrades('', minUsd)
            }}
          >
            全站大单
          </button>
          {whales.map((w) => (
            <div key={w.address} className="inline-flex items-center gap-1 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-elevated)] px-2 py-1 text-xs">
              <button
                className={`cursor-pointer border-0 bg-transparent p-0 text-xs font-medium ${selectedWhale === w.address ? 'text-[var(--accent)]' : 'text-[var(--ink)]'}`}
                title={w.address}
                onClick={() => {
                  setSelectedWhale(w.address)
                  loadTrades(w.address, 0)
                }}
              >
                {w.label}
              </button>
              <button className="cursor-pointer border-0 bg-transparent p-0 text-[var(--ink-3)] hover:text-[var(--danger)]" title="取消关注" onClick={() => removeWhale(w.address)}>
                <Trash2 className="h-3 w-3" />
              </button>
            </div>
          ))}
          <div className="flex items-center gap-1.5" style={{ marginLeft: 'auto' }}>
            <input className={`${inputCls} w-44`} placeholder="0x 钱包地址" value={newAddr} onChange={(e) => setNewAddr(e.target.value)} />
            <input className={`${inputCls} w-28`} placeholder="备注名" value={newLabel} onChange={(e) => setNewLabel(e.target.value)} />
            <button className="btn btn-ghost" onClick={() => addWhale()}>
              <Plus className="h-3.5 w-3.5" />
              关注
            </button>
          </div>
        </div>

        <div className="table-scroll">
          <table className="table static">
            <thead>
              <tr>
                <th>时间</th>
                <th>钱包</th>
                <th>方向</th>
                <th>市场 / 结果</th>
                <th className="num">价格</th>
                <th className="num">份数</th>
                <th className="num">成交额 (USDC)</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {trades.map((tr, i) => (
                <tr key={`${tr.txHash || tr.timestamp}-${i}`}>
                  <td className="mono small nowrap">{new Date(tr.timestamp * 1000).toLocaleTimeString('zh-CN', { hour12: false })}</td>
                  <td className="mono small nowrap" title={tr.wallet}>
                    {tr.whaleLabel ? (
                      <span className="tag tag-ok">{tr.whaleLabel}</span>
                    ) : tr.name ? (
                      <span>{tr.name}</span>
                    ) : (
                      <span className="text-[var(--ink-2)]">{shortAddr(tr.wallet)}</span>
                    )}
                  </td>
                  <td>
                    <span className={`tag ${tr.side === 'BUY' ? 'tag-hot' : 'tag-cold'}`}>{tr.side}</span>
                  </td>
                  <td className="max-w-[320px] truncate">
                    {tr.eventSlug ? (
                      <a href={`#/predict/${tr.eventSlug}`} className="text-[var(--accent)] no-underline hover:underline" title={tr.title}>
                        {tr.title || tr.slug}
                      </a>
                    ) : (
                      <span>{tr.title || tr.slug}</span>
                    )}
                    {tr.outcome && <span className="ml-1.5 tag tag-muted">{tr.outcome}</span>}
                  </td>
                  <td className="num mono small">{(tr.price * 100).toFixed(1)}¢</td>
                  <td className="num mono small">{tr.size.toFixed(0)}</td>
                  <td className="num mono small font-semibold">${tr.usdc.toLocaleString('en-US', { maximumFractionDigits: 0 })}</td>
                  <td className="num nowrap">
                    {!tr.whaleLabel && tr.wallet && (
                      <button className="btn btn-ghost" onClick={() => addWhale(tr.wallet, tr.name || shortAddr(tr.wallet))}>
                        + 关注
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {trades.length === 0 && (
                <tr>
                  <td colSpan={8} className="muted">
                    点击右上角“拉取全站大额成交”查看实时链上大单；或添加 0x 鲸鱼地址定点追踪其最新持仓动向。
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
