import { useCallback, useEffect, useState } from 'react'
import { Bell, Clock, Database, FileCheck2, Plus, Send, Trash2 } from 'lucide-react'
import { api, ApiError, type AuditEntry, type NotifyChannel, type NotifyKind, type NotifyState } from './api'
import { ConfigPanel } from './ConfigPanel'
import { useConfirm, useToast } from './ui/Feedback'

type Draft = { id: number; kind: string; name: string; config: Record<string, string>; masked: Record<string, string>; enabled: boolean }
type SettingsTab = 'config' | 'notify' | 'audit'

const ACTION_LABEL: Record<string, string> = {
  'pm.halt': 'PM 全局熔断',
  'pm.order': 'PM 下单',
  'pm.account.params': 'PM 账户风控',
  'params.save': '温度计参数',
  'sim.order': 'A 股模拟委托',
  'notify.settings': '推送与调度设置',
  'notify.channel': '推送渠道变更',
  'plugin.config': '插件配置',
  'config.save': '系统配置保存',
  'config.reset': '系统配置重置',
  'auth.setup': '初始化管理员口令',
  'auth.password': '修改管理员口令',
}

export default function Settings() {
  const [tab, setTab] = useState<SettingsTab>('config')
  const [state, setState] = useState<NotifyState | null>(null)
  const [audits, setAudits] = useState<AuditEntry[]>([])
  const [auditFilter, setAuditFilter] = useState('')
  const [schedStart, setSchedStart] = useState('17:30')
  const [schedDeadline, setSchedDeadline] = useState('22:00')
  const [schedMsg, setSchedMsg] = useState('')
  const [draft, setDraft] = useState<Draft | null>(null)
  const [tests, setTests] = useState<Record<number, { busy: boolean; error?: string; ok?: boolean }>>({})
  const [error, setError] = useState('')
  const confirm = useConfirm()
  const toast = useToast()

  const load = useCallback(() => {
    api.notify().then((s) => {
      setState(s)
      setSchedStart(s.scheduleStart || '17:30')
      setSchedDeadline(s.scheduleDeadline || '22:00')
    }, (e) => setError(e.message))
  }, [])
  const loadAudits = useCallback(() => {
    api.audits(auditFilter, 100).then(setAudits, () => {})
  }, [auditFilter])
  useEffect(load, [load])
  useEffect(loadAudits, [loadAudits])

  async function run(f: () => Promise<unknown>) {
    setError('')
    try {
      await f()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }

  if (!state) return <div className="page">{error && <div className="error" role="alert">{error}</div>}</div>

  const kindOf = (name: string) => state.kinds.find((k) => k.name === name)

  const startAdd = () => {
    const k = state.kinds[0]
    setDraft({ id: 0, kind: k.name, name: '', config: {}, masked: {}, enabled: true })
  }
  const startEdit = (c: NotifyChannel) => {
    const k = kindOf(c.kind)
    const config: Record<string, string> = {}
    for (const f of k?.fields ?? []) if (!f.secret) config[f.key] = c.config[f.key] ?? ''
    setDraft({ id: c.id, kind: c.kind, name: c.name, config, masked: c.config, enabled: c.enabled })
  }
  const save = () =>
    run(async () => {
      if (!draft) return
      await api.saveChannel(draft.id, { kind: draft.kind, name: draft.name, config: draft.config, enabled: draft.enabled })
      setDraft(null)
      load()
    })
  const remove = (c: NotifyChannel) =>
    run(async () => {
      const ok = await confirm({
        title: '删除推送渠道',
        message: `确认删除推送渠道「${c.name}」？`,
        confirmText: '删除',
        danger: true,
      })
      if (!ok) return
      await api.deleteChannel(c.id)
      toast.success(`已删除推送渠道「${c.name}」`)
      load()
    })
  const test = async (c: NotifyChannel) => {
    setTests((t) => ({ ...t, [c.id]: { busy: true } }))
    try {
      const r = await api.testChannel(c.id)
      setTests((t) => ({ ...t, [c.id]: { busy: false, ok: !r.error, error: r.error } }))
    } catch (e) {
      setTests((t) => ({ ...t, [c.id]: { busy: false, error: e instanceof Error ? e.message : String(e) } }))
    }
  }
  const toggle = (body: { signals?: boolean; weekly?: boolean; opsAlerts?: boolean }) =>
    run(async () => {
      if (body.signals) {
        const ok = await confirm({
          title: '开启实盘信号推送',
          message: '开启后，每个交易日收盘后的强提醒事件（武装、底部 / 顶部信号、估值档切换）会推送到已启用的渠道。\n\n推送内容只是客观指标描述，不构成投资建议。确定开启？',
          confirmText: '确认开启',
        })
        if (!ok) return
      }
      setState(await api.saveNotifySettings(body))
      toast.success('推送开关已更新')
      loadAudits()
    })
  const saveSchedule = () =>
    run(async () => {
      setSchedMsg('')
      setState(await api.saveNotifySettings({ scheduleStart: schedStart, scheduleDeadline: schedDeadline }))
      setSchedMsg('已保存调度窗口')
      loadAudits()
    })

  const editingKind = draft ? kindOf(draft.kind) : undefined

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>系统设置</h1>
          <p className="sub">全部系统参数已迁移至本地 SQLite 数据库（零 config.yaml 启动），并支持推送渠道、调度窗口与审计日志管理。</p>
        </div>
      </div>
      {error && <div className="error" role="alert">{error}</div>}

      <div className="tabs-bar" role="tablist">
        <button type="button" role="tab" aria-selected={tab === 'config'} className={`tab-btn ${tab === 'config' ? 'on' : ''}`} onClick={() => setTab('config')}>
          <Database className="h-4 w-4" />
          系统配置（数据库）
        </button>
        <button type="button" role="tab" aria-selected={tab === 'notify'} className={`tab-btn ${tab === 'notify' ? 'on' : ''}`} onClick={() => setTab('notify')}>
          <Bell className="h-4 w-4" />
          推送与调度
        </button>
        <button type="button" role="tab" aria-selected={tab === 'audit'} className={`tab-btn ${tab === 'audit' ? 'on' : ''}`} onClick={() => setTab('audit')}>
          <FileCheck2 className="h-4 w-4" />
          审计日志
          {audits.length > 0 && <span className="tag tag-muted">{audits.length}</span>}
        </button>
      </div>

      {tab === 'config' && <ConfigPanel onSaved={loadAudits} />}

      {tab === 'notify' && (
      <>
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <Bell className="h-4 w-4 text-[var(--accent)]" />
          <strong>推送开关</strong>
          <span className="muted">两项默认关闭；“测试”发送不受开关限制</span>
        </div>
        <div className="divide-y divide-[var(--line)]">
          <label className="flex cursor-pointer items-start justify-between gap-4 px-5 py-4 transition-colors hover:bg-[var(--bg-subtle)]">
            <div>
              <div className="text-sm font-medium text-[var(--ink)]">实盘信号推送</div>
              <div className="mt-0.5 text-xs text-[var(--ink-2)]">
                {state.signals
                  ? `已开启：只推送 ${state.since || '开启时'} 之后交易日的武装、底部 / 顶部信号、估值档切换`
                  : '交易日收盘同步完成后，推送当日的武装、底部 / 顶部信号、估值档切换'}
              </div>
            </div>
            <input
              type="checkbox"
              className="mt-1 h-4 w-4 accent-[var(--accent)]"
              checked={state.signals}
              onChange={(e) => toggle({ signals: e.target.checked })}
            />
          </label>
          <label className="flex cursor-pointer items-start justify-between gap-4 px-5 py-4 transition-colors hover:bg-[var(--bg-subtle)]">
            <div>
              <div className="text-sm font-medium text-[var(--ink)]">每周五周报</div>
              <div className="mt-0.5 text-xs text-[var(--ink-2)]">
                每周五收盘同步完成后，推送本周温度走势、估值档、各维度得分（周五休市的那周不发）
              </div>
            </div>
            <input
              type="checkbox"
              className="mt-1 h-4 w-4 accent-[var(--accent)]"
              checked={state.weekly}
              onChange={(e) => toggle({ weekly: e.target.checked })}
            />
          </label>
          <label className="flex cursor-pointer items-start justify-between gap-4 px-5 py-4 transition-colors hover:bg-[var(--bg-subtle)]">
            <div>
              <div className="text-sm font-medium text-[var(--ink)]">系统运维告警</div>
              <div className="mt-0.5 text-xs text-[var(--ink-2)]">
                盘口采集连续失败中断/恢复、A 股每日同步截止仍失败、Polymarket 全局熔断切换时立即推送（30 分钟内同类去重）
              </div>
            </div>
            <input
              type="checkbox"
              className="mt-1 h-4 w-4 accent-[var(--accent)]"
              checked={state.opsAlerts}
              onChange={(e) => toggle({ opsAlerts: e.target.checked })}
            />
          </label>
        </div>
      </section>

      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <Clock className="h-4 w-4 text-[var(--accent)]" />
          <strong>A 股每日调度窗口</strong>
          <span className="muted">北京时间工作日自动同步并重试，修改后立即生效</span>
        </div>
        <div className="flex flex-wrap items-end gap-4 px-5 py-4">
          <label className="field">
            <span>开始时间（HH:MM）</span>
            <input className="mono" value={schedStart} onChange={(e) => setSchedStart(e.target.value)} placeholder="17:30" />
          </label>
          <label className="field">
            <span>截止时间（HH:MM）</span>
            <input className="mono" value={schedDeadline} onChange={(e) => setSchedDeadline(e.target.value)} placeholder="22:00" />
          </label>
          <button className="btn btn-primary" onClick={saveSchedule}>保存时间</button>
          {schedMsg && <span className="ok-text small">{schedMsg}</span>}
        </div>
      </section>

      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <strong>推送渠道</strong>
          <span className="muted">钉钉、飞书、企业微信直连；Telegram 走 config.yaml 的 http_proxy</span>
          <button className="btn btn-primary" style={{ marginLeft: 'auto' }} onClick={startAdd} disabled={!!draft}>
            <Plus className="h-3.5 w-3.5" />
            添加渠道
          </button>
        </div>
        {state.channels.length === 0 ? (
          <div className="empty">还没有推送渠道。</div>
        ) : (
          <table className="table static">
            <thead>
              <tr><th>名称</th><th>类型</th><th>配置</th><th>状态</th><th></th></tr>
            </thead>
            <tbody>
              {state.channels.map((c) => {
                const t = tests[c.id]
                return (
                  <tr key={c.id}>
                    <td className="metric-name">{c.name}</td>
                    <td className="muted">{c.kindLabel}</td>
                    <td className="mono muted small">
                      {(kindOf(c.kind)?.fields ?? []).map((f) => (
                        <div key={f.key}>{f.label}：{c.config[f.key] || '—'}</div>
                      ))}
                    </td>
                    <td>
                      <span className={`tag ${c.enabled ? 'tag-ok' : 'tag-muted'}`}>{c.enabled ? '启用' : '停用'}</span>
                      {t && !t.busy && (t.ok ? <div className="ok-text small">测试发送成功</div> : <div className="bad-text small">{t.error}</div>)}
                    </td>
                    <td className="num nowrap">
                      <button className="btn btn-ghost" disabled={t?.busy} onClick={() => test(c)}>
                        <Send className="h-3.5 w-3.5" />
                        {t?.busy ? '发送中…' : '测试'}
                      </button>
                      <button className="btn btn-ghost" disabled={!!draft} onClick={() => startEdit(c)}>编辑</button>
                      <button className="btn btn-ghost text-[var(--bad)]" onClick={() => remove(c)}>
                        <Trash2 className="h-3.5 w-3.5" />
                        删除
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
        {draft && (
          <div className="channel-form">
            <div className="form-grid">
              <label className="field">
                <span>类型</span>
                <select value={draft.kind} disabled={draft.id !== 0} onChange={(e) => setDraft({ ...draft, kind: e.target.value, config: {}, masked: {} })}>
                  {state.kinds.map((k) => <option key={k.name} value={k.name}>{k.label}</option>)}
                </select>
              </label>
              <label className="field">
                <span>名称</span>
                <input value={draft.name} placeholder={editingKind?.label} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
              </label>
              {editingKind?.fields.map((f) => (
                <ConfigField key={f.key} field={f} value={draft.config[f.key] ?? ''} masked={draft.masked[f.key]}
                  onChange={(v) => setDraft({ ...draft, config: { ...draft.config, [f.key]: v } })} />
              ))}
            </div>
            <div className="form-foot">
              <label className="field-check">
                <input type="checkbox" checked={draft.enabled} onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })} />
                启用
              </label>
              <span style={{ flex: 1 }} />
              <button className="btn" onClick={() => setDraft(null)}>取消</button>
              <button className="btn btn-primary" onClick={save}>保存</button>
            </div>
          </div>
        )}
      </section>

      <section className="panel">
        <div className="panel-head"><strong>推送记录</strong><span className="muted">最近 20 条；同一条消息只推送一次</span></div>
        {state.logs.length === 0 ? (
          <div className="empty">暂无推送记录。</div>
        ) : (
          <table className="table static">
            <thead><tr><th>时间</th><th>消息</th><th>结果</th></tr></thead>
            <tbody>
              {state.logs.map((l) => (
                <tr key={l.key}>
                  <td className="mono nowrap">{new Date(l.sentAt).toLocaleString('zh-CN', { hour12: false })}</td>
                  <td>{l.title}</td>
                  <td className="muted small">{l.result}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
      </>
      )}

      {tab === 'audit' && (
      <section className="panel">
        <div className="panel-head">
          <FileCheck2 className="h-4 w-4 text-[var(--accent)]" />
          <strong>审计日志</strong>
          <span className="muted">只追加不删改，记录配置变更、参数生效、熔断、委托与推送操作</span>
          <select
            style={{ marginLeft: 'auto' }}
            className="h-8 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-2 text-xs text-[var(--ink)]"
            value={auditFilter}
            onChange={(e) => setAuditFilter(e.target.value)}
          >
            <option value="">全部操作</option>
            <option value="config">系统配置（config.*）</option>
            <option value="auth">口令与鉴权（auth.*）</option>
            <option value="pm">Polymarket（pm.*）</option>
            <option value="params">温度计参数</option>
            <option value="sim">A 股模拟盘</option>
            <option value="notify">推送与调度</option>
            <option value="plugin">插件配置</option>
          </select>
        </div>
        {audits.length === 0 ? (
          <div className="empty">暂无审计记录。</div>
        ) : (
          <div className="scroll-y">
            <table className="table static">
              <thead><tr><th>时间</th><th>操作</th><th>对象</th><th>详情</th></tr></thead>
              <tbody>
                {audits.map((a) => (
                  <tr key={a.id}>
                    <td className="mono nowrap small">{new Date(a.at).toLocaleString('zh-CN', { hour12: false })}</td>
                    <td><span className="tag tag-muted">{ACTION_LABEL[a.action] ?? a.action}</span></td>
                    <td className="mono small">{a.target || '—'}</td>
                    <td className="mono muted small">{JSON.stringify(a.detail)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      )}
    </div>
  )
}

function ConfigField({ field, value, masked, onChange }: { field: NotifyKind['fields'][number]; value: string; masked?: string; onChange: (v: string) => void }) {
  const placeholder = field.secret && masked ? `已设置（${masked}），留空保持不变` : ''
  return (
    <label className={`field ${field.key === 'webhook' ? 'span-2' : ''}`}>
      <span>{field.label}{field.required && <span className="bad-text"> *</span>}</span>
      <input
        type={field.secret ? 'password' : 'text'}
        autoComplete="off"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
      {field.help && !(field.secret && masked) && <small className="muted">{field.help}</small>}
    </label>
  )
}
