import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, type NotifyChannel, type NotifyKind, type NotifyState } from './api'

type Draft = { id: number; kind: string; name: string; config: Record<string, string>; masked: Record<string, string>; enabled: boolean }

export default function Settings() {
  const [state, setState] = useState<NotifyState | null>(null)
  const [draft, setDraft] = useState<Draft | null>(null)
  const [tests, setTests] = useState<Record<number, { busy: boolean; error?: string; ok?: boolean }>>({})
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api.notify().then(setState, (e) => setError(e.message))
  }, [])
  useEffect(load, [load])

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
      if (!confirm(`删除推送渠道“${c.name}”？`)) return
      await api.deleteChannel(c.id)
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
  const toggle = (body: { signals?: boolean; weekly?: boolean }) =>
    run(async () => {
      if (body.signals && !confirm('开启后，每个交易日收盘后的强提醒事件（武装、底部 / 顶部信号、估值档切换）会推送到已启用的渠道。\n\n当前参数的回测尚未在样本外跑赢估值档基线，推送内容只是指标描述。确定开启？')) return
      setState(await api.saveNotifySettings(body))
    })

  const editingKind = draft ? kindOf(draft.kind) : undefined

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>设置</h1>
          <p className="sub">推送渠道与推送开关。Webhook、密钥等敏感信息保存后只显示末 4 位。</p>
        </div>
      </div>
      {error && <div className="error" role="alert">{error}</div>}

      <section className="panel settings-section">
        <div className="panel-head"><strong>推送开关</strong><span className="muted">两项默认关闭；“测试”发送不受开关限制</span></div>
        <label className="field-check">
          <input type="checkbox" checked={state.signals} onChange={(e) => toggle({ signals: e.target.checked })} />
          实盘信号推送
          <span className="muted">
            {state.signals
              ? `已开启：只推送 ${state.since || '开启时'} 之后交易日的武装、底部 / 顶部信号、估值档切换`
              : '交易日收盘同步完成后，推送当日的武装、底部 / 顶部信号、估值档切换'}
          </span>
        </label>
        <label className="field-check">
          <input type="checkbox" checked={state.weekly} onChange={(e) => toggle({ weekly: e.target.checked })} />
          周报
          <span className="muted">每周五收盘同步完成后，推送本周温度走势、估值档、各维度得分（周五休市的那周不发）</span>
        </label>
      </section>

      <section className="panel">
        <div className="panel-head">
          <strong>推送渠道</strong>
          <span className="muted">钉钉、飞书、企业微信直连；Telegram 走 config.yaml 的 http_proxy</span>
          <button className="btn" style={{ marginLeft: 'auto' }} onClick={startAdd} disabled={!!draft}>添加渠道</button>
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
                      <button className="btn btn-ghost" disabled={t?.busy} onClick={() => test(c)}>{t?.busy ? '发送中…' : '测试'}</button>
                      <button className="btn btn-ghost" disabled={!!draft} onClick={() => startEdit(c)}>编辑</button>
                      <button className="btn btn-ghost" onClick={() => remove(c)}>删除</button>
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
