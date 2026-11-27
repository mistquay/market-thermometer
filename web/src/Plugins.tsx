import { useCallback, useEffect, useState } from 'react'
import { Play, RefreshCw, SlidersHorizontal } from 'lucide-react'
import { api, ApiError, type PluginInfo } from './api'

const KIND_ORDER = ['strategy', 'derived', 'metric_source', 'stock_source', 'fund_source', 'crypto_source', 'prediction_source', 'notifier', 'broker']

type TestResult = { busy: boolean; text?: string; error?: string }

export default function Plugins() {
  const [list, setList] = useState<PluginInfo[] | null>(null)
  const [error, setError] = useState('')
  const [tests, setTests] = useState<Record<string, TestResult>>({})

  const load = useCallback(() => {
    api.plugins().then(setList, (e) => setError(e.message))
  }, [])
  useEffect(load, [load])

  const test = async (p: PluginInfo) => {
    setTests((t) => ({ ...t, [p.id]: { busy: true } }))
    try {
      const r = await api.testPlugin(p.kind, p.name)
      const text = `${r.target}：${r.count} 条，耗时 ${r.elapsedMs} ms${r.last ? `，最新 ${JSON.stringify(r.last)}` : ''}`
      setTests((t) => ({ ...t, [p.id]: { busy: false, text: r.error ? undefined : text, error: r.error } }))
    } catch (e) {
      setTests((t) => ({ ...t, [p.id]: { busy: false, error: e instanceof Error ? e.message : String(e) } }))
    }
    load()
  }

  const groups = KIND_ORDER.map((k) => (list ?? []).filter((p) => p.kind === k)).filter((g) => g.length > 0)

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>插件</h1>
          <p className="sub">
            内置插件的配置在 config.yaml 中修改，这里只读；外部插件（独立进程，协议见 docs/plugin-protocol.md）可在这里启停和修改配置。
            外部插件的可执行文件只能在 config.yaml 中配置。
          </p>
        </div>
        <div className="actions">
          <button className="btn" onClick={load}>
            <RefreshCw className="h-3.5 w-3.5" />
            刷新
          </button>
        </div>
      </div>
      {error && <div className="error" role="alert">{error}</div>}

      {groups.map((g) => (
        <section key={g[0].kind} className="panel">
          <div className="panel-head"><strong>{g[0].kindLabel}</strong><span className="muted">{g.length} 个</span></div>
          <div className="plugin-list">
            {g.map((p) => <PluginCard key={p.id} p={p} test={tests[p.id]} onTest={() => test(p)} onSaved={load} />)}
          </div>
        </section>
      ))}
    </div>
  )
}

function PluginCard({ p, test, onTest, onSaved }: { p: PluginInfo; test?: TestResult; onTest: () => void; onSaved: () => void }) {
  const [editing, setEditing] = useState(false)
  const [enabled, setEnabled] = useState(p.enabled)
  const [values, setValues] = useState<Record<string, string>>({})
  const [error, setError] = useState('')
  const [showLog, setShowLog] = useState(false)

  const startEdit = () => {
    const v: Record<string, string> = {}
    for (const f of p.config ?? []) if (!f.secret) v[f.key] = p.values[f.key] ?? ''
    setValues(v)
    setEnabled(p.enabled)
    setEditing(true)
  }
  const save = async () => {
    setError('')
    try {
      await api.savePlugin(p.kind, p.name, { enabled, config: values })
      setEditing(false)
      onSaved()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }

  return (
    <div className="plugin-card">
      <div className="plugin-head">
        <strong>{p.label}</strong>
        <code className="metric-id">{p.name}</code>
        <span className="tag tag-muted">{p.builtin ? '内置' : `外部 ${p.version}`}</span>
        <span className={`tag ${p.enabled ? 'tag-ok' : 'tag-muted'}`}>{p.state}</span>
        <span style={{ flex: 1 }} />
        {p.testable && (
          <button className="btn btn-ghost" disabled={test?.busy} onClick={onTest}>
            <Play className="h-3.5 w-3.5" />
            {test?.busy ? '测试中…' : '测试调用'}
          </button>
        )}
        {p.editable && !editing && (
          <button className="btn btn-ghost" onClick={startEdit}>
            <SlidersHorizontal className="h-3.5 w-3.5" />
            设置
          </button>
        )}
      </div>
      {p.description && <p className="muted small plugin-desc">{p.description}</p>}
      {test && !test.busy && (test.error ? <div className="bad-text small">{test.error}</div> : <div className="ok-text small">{test.text}</div>)}
      {p.metrics && p.metrics.length > 0 && (
        <div className="plugin-metrics">
          {p.metrics.map((m) => <span key={m.id} className="chip" title={m.id}>{m.name || m.id}</span>)}
        </div>
      )}
      {p.kind === 'notifier' && (p.config ?? []).length > 0 && (
        <div className="muted small">渠道字段：{(p.config ?? []).map((f) => f.label).join('、')}（在设置页按渠道填写）</div>
      )}
      {!editing && p.kind !== 'notifier' && (p.config ?? []).length > 0 && (
        <div className="plugin-values small">
          {(p.config ?? []).map((f) => (
            <span key={f.key}><span className="muted">{f.label}：</span><span className="mono">{p.values[f.key] || '（默认）'}</span></span>
          ))}
        </div>
      )}
      {editing && (
        <div className="channel-form">
          <label className="field-check">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> 启用
          </label>
          <div className="form-grid">
            {(p.config ?? []).map((f) => (
              <label key={f.key} className="field">
                <span>{f.label}{f.required && <span className="bad-text"> *</span>}</span>
                <input
                  type={f.secret ? 'password' : f.type === 'number' ? 'number' : 'text'}
                  autoComplete="off"
                  value={values[f.key] ?? ''}
                  placeholder={f.secret && p.values[f.key] ? `已设置（${p.values[f.key]}），留空保持不变` : ''}
                  onChange={(e) => setValues({ ...values, [f.key]: e.target.value })}
                />
                {f.help && <small className="muted">{f.help}</small>}
              </label>
            ))}
          </div>
          {error && <div className="error">{error}</div>}
          <div className="form-foot">
            <span className="muted small">保存后插件进程会重启，下次调用时按新配置初始化。</span>
            <span style={{ flex: 1 }} />
            <button className="btn" onClick={() => setEditing(false)}>取消</button>
            <button className="btn btn-primary" onClick={save}>保存</button>
          </div>
        </div>
      )}
      {p.process && (
        <div className="plugin-proc small">
          <span className="muted">命令：</span><code>{p.process.command.join(' ')}</code>
          <span className="muted"> · 10 分钟内启动 {p.process.starts} 次</span>
          {p.process.lastError && <div className="bad-text">最近错误：{p.process.lastError}</div>}
          {p.process.stderr.length > 0 && (
            <>
              <button className="link" onClick={() => setShowLog(!showLog)}>{showLog ? '收起日志' : `插件日志（${p.process.stderr.length} 行）`}</button>
              {showLog && <pre className="log">{p.process.stderr.join('\n')}</pre>}
            </>
          )}
        </div>
      )}
    </div>
  )
}
