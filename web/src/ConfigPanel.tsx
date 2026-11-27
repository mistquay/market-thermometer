import { useCallback, useEffect, useState } from 'react'
import { Database, KeyRound, RotateCcw, Save, SlidersHorizontal } from 'lucide-react'
import { api, ApiError, type AppConfig, type AppConfigResp } from './api'
import { useConfirm, useToast } from './ui/Feedback'

const inputCls =
  'h-8 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-2.5 text-xs mono text-[var(--ink)] focus:border-[var(--accent)] focus:outline-none'

const splitCSV = (s: string) =>
  s
    .split(/[,，\n]/)
    .map((x) => x.trim())
    .filter(Boolean)

const joinCSV = (xs?: string[] | null) => (xs ?? []).join(', ')

const SOURCE_LABEL: Record<string, string> = {
  db: '数据库配置（app_config）',
  default: '内置零配置默认',
  imported: '由旧版 config.yaml 自动迁移入库',
  yaml: '命令行 -c YAML 覆盖',
}

export function ConfigPanel({ onSaved }: { onSaved?: () => void }) {
  const [resp, setResp] = useState<AppConfigResp | null>(null)
  const [cfg, setCfg] = useState<AppConfig | null>(null)
  const [routesText, setRoutesText] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [warn, setWarn] = useState('')
  const [error, setError] = useState('')
  const confirm = useConfirm()
  const toast = useToast()

  // 密码修改
  const [oldPw, setOldPw] = useState('')
  const [newPw, setNewPw] = useState('')
  const [confirmPw, setConfirmPw] = useState('')
  const [pwMsg, setPwMsg] = useState('')

  const load = useCallback(() => {
    api.appConfig().then(
      (r) => {
        setResp(r)
        setCfg(structuredClone(r.config))
        setRoutesText(JSON.stringify(r.config.sources.routes ?? {}, null, 2))
      },
      (e: Error) => setError(e.message),
    )
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const save = async () => {
    if (!cfg) return
    setBusy(true)
    setError('')
    setMsg('')
    setWarn('')
    try {
      let parsedRoutes = cfg.sources.routes
      if (routesText.trim()) {
        parsedRoutes = JSON.parse(routesText) as Record<string, string[]>
      }
      const next: AppConfig = { ...cfg, sources: { ...cfg.sources, routes: parsedRoutes } }
      const r = await api.saveAppConfig(next)
      setCfg(structuredClone(r.config))
      const savedMsg =
        r.hotApplied && r.hotApplied.length > 0
          ? `配置已写入数据库；热更新已立即生效：${r.hotApplied.join(', ')}`
          : '配置已写入数据库（app_config）。'
      setMsg(savedMsg)
      toast.success(savedMsg)
      if (r.restartRequired) {
        setWarn('包含端口/代理/数据源/WS 通道变更：已持久化到数据库，重启 mt serve 后生效（无需 config.yaml）。')
      }
      load()
      onSaved?.()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const reset = async () => {
    const ok = await confirm({
      title: '恢复内置零配置默认值',
      message: '确认将数据库系统配置重置为内置零配置默认值？（将保留管理员口令与历史数据）',
      confirmText: '确认重置',
      danger: true,
    })
    if (!ok) return
    setBusy(true)
    setError('')
    setMsg('')
    setWarn('')
    try {
      await api.resetAppConfig()
      setMsg('已重置为内置零配置默认值。')
      toast.success('已重置为内置零配置默认值')
      setWarn('部分启动级参数需重启 mt serve 后生效。')
      load()
      onSaved?.()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const changePassword = async () => {
    setError('')
    setPwMsg('')
    if (newPw.trim().length < 8) return setError('新口令长度至少 8 位')
    if (newPw !== confirmPw) return setError('两次输入的新口令不一致')
    try {
      await api.changePassword(oldPw, newPw)
      setOldPw('')
      setNewPw('')
      setConfirmPw('')
      setPwMsg('管理员口令已更新并存入数据库。')
      onSaved?.()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e))
    }
  }

  if (!resp || !cfg) {
    return <div className="panel empty">{error ? <span className="bad-text">{error}</span> : '加载系统配置中…'}</div>
  }

  const def = resp.defaults

  return (
    <div className="flex flex-col gap-4">
      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}
      {msg && <div className="ok-note">{msg}</div>}
      {warn && <div className="notice">{warn}</div>}

      {/* 顶栏：来源与存储路径白盒展示 */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <div className="flex flex-wrap items-center gap-2">
            <Database className="h-4 w-4 shrink-0 text-[var(--accent)]" />
            <strong>零配置数据库化配置中心（app_config）</strong>
            <span className="tag tag-ok">{SOURCE_LABEL[resp.source] ?? resp.source}</span>
          </div>
          <div className="flex flex-wrap items-center gap-2 sm:ml-auto">
            <button className="btn btn-ghost" disabled={busy} onClick={reset}>
              <RotateCcw className="h-3.5 w-3.5" />
              恢复内置默认
            </button>
            <button className="btn btn-primary" disabled={busy} onClick={save}>
              <Save className="h-3.5 w-3.5" />
              {busy ? '保存中…' : '保存全部配置'}
            </button>
          </div>
        </div>
        <div className="grid grid-cols-1 gap-2 bg-[var(--bg-subtle)] px-5 py-3 text-xs text-[var(--ink-2)] md:grid-cols-3">
          <div>
            主库路径：<span className="mono text-[var(--ink)]">{resp.dbPath}</span>
          </div>
          <div>
            高频盘口库：<span className="mono text-[var(--ink)]">{resp.tickDbPath}</span>
          </div>
          <div>
            最近保存：<span className="mono text-[var(--ink)]">{resp.updatedAt ? new Date(resp.updatedAt).toLocaleString('zh-CN', { hour12: false }) : '使用内置默认'}</span>
          </div>
        </div>
      </section>

      {/* 1. Polymarket 盘口采集与公允概率策略 */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <SlidersHorizontal className="h-4 w-4 shrink-0 text-[var(--accent)]" />
          <strong>Polymarket 盘口采集与影子策略参数</strong>
          <span className="tag tag-cold">minEdge / Student-t df 支持热更新</span>
        </div>
        <div className="grid grid-cols-1 gap-3.5 p-5 sm:grid-cols-2 lg:grid-cols-4">
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>数据源插件</span>
            <input className={inputCls} value={cfg.polymarket.source} onChange={(e) => setCfg({ ...cfg, polymarket: { ...cfg.polymarket, source: e.target.value } })} />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{def.polymarket.source}（留空停用）</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>
              Student-t 肥尾自由度 df <span className="tag tag-ok ml-1">热更新</span>
            </span>
            <input
              type="number"
              step={1}
              min={0}
              className={inputCls}
              value={cfg.polymarket.strategy.df}
              onChange={(e) =>
                setCfg({
                  ...cfg,
                  polymarket: { ...cfg.polymarket, strategy: { ...cfg.polymarket.strategy, df: e.target.valueAsNumber || 0 } },
                })
              }
            />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{def.polymarket.strategy.df}（0=正态，推荐 5–8 缓解尾部过自信）</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>
              最小记录 Edge (美元/股) <span className="tag tag-ok ml-1">热更新</span>
            </span>
            <input
              type="number"
              step={0.01}
              min={0}
              max={0.5}
              className={inputCls}
              value={cfg.polymarket.strategy.minEdge}
              onChange={(e) =>
                setCfg({
                  ...cfg,
                  polymarket: { ...cfg.polymarket, strategy: { ...cfg.polymarket.strategy, minEdge: e.target.valueAsNumber || 0 } },
                })
              }
            />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{def.polymarket.strategy.minEdge}（即 3¢）</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>盘口采集间隔 (ms)</span>
            <input
              type="number"
              step={500}
              min={0}
              className={inputCls}
              value={cfg.polymarket.book.everyMs}
              onChange={(e) =>
                setCfg({
                  ...cfg,
                  polymarket: { ...cfg.polymarket, book: { ...cfg.polymarket.book, everyMs: e.target.valueAsNumber || 0 } },
                })
              }
            />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{def.polymarket.book.everyMs}ms（0=不采集，重启生效）</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>每边深度档位 / 保留天数</span>
            <div className="flex gap-1.5">
              <input
                type="number"
                className={`${inputCls} w-full`}
                value={cfg.polymarket.book.levels}
                onChange={(e) =>
                  setCfg({ ...cfg, polymarket: { ...cfg.polymarket, book: { ...cfg.polymarket.book, levels: e.target.valueAsNumber || 10 } } })
                }
              />
              <input
                type="number"
                className={`${inputCls} w-full`}
                value={cfg.polymarket.book.keepDays}
                onChange={(e) =>
                  setCfg({ ...cfg, polymarket: { ...cfg.polymarket, book: { ...cfg.polymarket.book, keepDays: e.target.valueAsNumber || 14 } } })
                }
              />
            </div>
            <small className="text-[11px] text-[var(--ink-3)]">默认：{def.polymarket.book.levels} 档 / {def.polymarket.book.keepDays} 天</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)] sm:col-span-2 lg:col-span-3">
            <span>同步与采集系列（逗号分隔）</span>
            <input
              className={inputCls}
              value={joinCSV(cfg.polymarket.series)}
              onChange={(e) => setCfg({ ...cfg, polymarket: { ...cfg.polymarket, series: splitCSV(e.target.value) } })}
            />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{joinCSV(def.polymarket.series)}</small>
          </label>
        </div>
        <div className="flex flex-wrap gap-6 border-t border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-3 text-xs">
          <label className="flex items-center gap-2 text-[var(--ink)]">
            <input
              type="checkbox"
              checked={cfg.polymarket.book.ws}
              onChange={(e) => setCfg({ ...cfg, polymarket: { ...cfg.polymarket, book: { ...cfg.polymarket.book, ws: e.target.checked } } })}
            />
            启用 WebSocket 实时盘口与币安现货推送（断线自动回退 REST）
          </label>
          <label className="flex items-center gap-2 text-[var(--ink)]">
            <input
              type="checkbox"
              checked={cfg.polymarket.strategy.shadow}
              onChange={(e) => setCfg({ ...cfg, polymarket: { ...cfg.polymarket, strategy: { ...cfg.polymarket.strategy, shadow: e.target.checked } } })}
            />
            启用公允概率影子策略
          </label>
          <label className="flex items-center gap-2 text-[var(--ink)]">
            <input
              type="checkbox"
              checked={cfg.polymarket.proxy}
              onChange={(e) => setCfg({ ...cfg, polymarket: { ...cfg.polymarket, proxy: e.target.checked } })}
            />
            Polymarket 经 HTTP 代理出站
          </label>
        </div>
      </section>

      {/* 2. 服务监听、网络代理与加密货币配置 */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <strong>服务网络与加密货币行情</strong>
          <span className="muted text-xs">保存后重启 mt serve 生效（也可用命令行 -listen 临时覆盖监听端口）</span>
        </div>
        <div className="grid grid-cols-1 gap-3.5 p-5 sm:grid-cols-2 lg:grid-cols-4">
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>HTTP 监听地址</span>
            <input className={inputCls} value={cfg.listen} onChange={(e) => setCfg({ ...cfg, listen: e.target.value })} />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{def.listen}</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>出站 HTTP 代理（可选）</span>
            <input className={inputCls} placeholder="如 http://127.0.0.1:7890" value={cfg.httpProxy} onChange={(e) => setCfg({ ...cfg, httpProxy: e.target.value })} />
            <small className="text-[11px] text-[var(--ink-3)]">用于 Telegram 及勾选代理的海外源，A 股源始终直连</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>全量回填起始日</span>
            <input className={inputCls} value={cfg.backfillStart} onChange={(e) => setCfg({ ...cfg, backfillStart: e.target.value })} />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{def.backfillStart}</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>加密行情同步间隔 (分)</span>
            <input
              type="number"
              min={1}
              className={inputCls}
              value={cfg.crypto.everyMin}
              onChange={(e) => setCfg({ ...cfg, crypto: { ...cfg.crypto, everyMin: e.target.valueAsNumber || 15 } })}
            />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{def.crypto.everyMin} 分钟</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>加密数据源顺序</span>
            <input className={inputCls} value={joinCSV(cfg.crypto.sources)} onChange={(e) => setCfg({ ...cfg, crypto: { ...cfg.crypto, sources: splitCSV(e.target.value) } })} />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{joinCSV(def.crypto.sources)}</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)] sm:col-span-2">
            <span>加密交易对（逗号分隔）</span>
            <input className={inputCls} value={joinCSV(cfg.crypto.symbols)} onChange={(e) => setCfg({ ...cfg, crypto: { ...cfg.crypto, symbols: splitCSV(e.target.value) } })} />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{joinCSV(def.crypto.symbols)}</small>
          </label>
          <label className="flex items-center gap-2 pt-5 text-xs text-[var(--ink)]">
            <input
              type="checkbox"
              checked={cfg.crypto.proxy}
              onChange={(e) => setCfg({ ...cfg, crypto: { ...cfg.crypto, proxy: e.target.checked } })}
            />
            加密行情经 HTTP 代理
          </label>
        </div>
      </section>

      {/* 3. A 股数据源路由 */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <strong>A 股指标、个股与基金数据源路由</strong>
          <span className="muted text-xs">控制数据同步时的主源与回退顺序（重启生效）</span>
        </div>
        <div className="grid grid-cols-1 gap-3.5 p-5 sm:grid-cols-2 lg:grid-cols-4">
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>启用的指标插件</span>
            <input className={inputCls} value={joinCSV(cfg.sources.enabled)} onChange={(e) => setCfg({ ...cfg, sources: { ...cfg.sources, enabled: splitCSV(e.target.value) } })} />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{joinCSV(def.sources.enabled)}</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>个股日线源（优先级）</span>
            <input className={inputCls} value={joinCSV(cfg.stocks.sources)} onChange={(e) => setCfg({ ...cfg, stocks: { ...cfg.stocks, sources: splitCSV(e.target.value) } })} />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{joinCSV(def.stocks.sources)}</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>场内基金源 / 并发数</span>
            <div className="flex gap-1.5">
              <input className={`${inputCls} flex-1`} value={joinCSV(cfg.stocks.fundSources)} onChange={(e) => setCfg({ ...cfg, stocks: { ...cfg.stocks, fundSources: splitCSV(e.target.value) } })} />
              <input
                type="number"
                min={1}
                max={16}
                className={`${inputCls} w-16`}
                value={cfg.stocks.concurrency}
                onChange={(e) => setCfg({ ...cfg, stocks: { ...cfg.stocks, concurrency: e.target.valueAsNumber || 4 } })}
              />
            </div>
            <small className="text-[11px] text-[var(--ink-3)]">默认：{joinCSV(def.stocks.fundSources)} · 并发 {def.stocks.concurrency}</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>场外基金净值源</span>
            <input className={inputCls} value={joinCSV(cfg.funds.sources)} onChange={(e) => setCfg({ ...cfg, funds: { ...cfg.funds, sources: splitCSV(e.target.value) } })} />
            <small className="text-[11px] text-[var(--ink-3)]">默认：{joinCSV(def.funds.sources)}</small>
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)] sm:col-span-2 lg:col-span-4">
            <span>指标路由表 JSON（metric → 数据源优先级，空数组表示不同步）</span>
            <textarea
              rows={6}
              className="w-full rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] p-2.5 text-xs mono text-[var(--ink)] focus:border-[var(--accent)] focus:outline-none"
              value={routesText}
              onChange={(e) => setRoutesText(e.target.value)}
            />
          </label>
        </div>
      </section>

      {/* 4. 管理员口令修改 */}
      <section className="panel overflow-hidden">
        <div className="panel-head border-b border-[var(--line)]">
          <KeyRound className="h-4 w-4 text-[var(--accent)]" />
          <strong>管理员访问口令（bcrypt 存库）</strong>
          <span className="muted text-xs">不再依赖 config.yaml 中的 admin_password_hash</span>
        </div>
        <div className="flex flex-wrap items-end gap-3 p-5">
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>当前口令</span>
            <input type="password" className={`${inputCls} w-44`} value={oldPw} onChange={(e) => setOldPw(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>新口令（至少 8 位）</span>
            <input type="password" className={`${inputCls} w-44`} value={newPw} onChange={(e) => setNewPw(e.target.value)} />
          </label>
          <label className="flex flex-col gap-1 text-xs text-[var(--ink-2)]">
            <span>确认新口令</span>
            <input type="password" className={`${inputCls} w-44`} value={confirmPw} onChange={(e) => setConfirmPw(e.target.value)} />
          </label>
          <button className="btn btn-primary" disabled={!newPw || !confirmPw} onClick={changePassword}>
            更新口令
          </button>
          {pwMsg && <span className="ok-text text-xs">{pwMsg}</span>}
        </div>
      </section>

      {/* 外部进程插件安全边界说明 */}
      {cfg.plugins && cfg.plugins.length > 0 && (
        <section className="panel overflow-hidden">
          <div className="panel-head border-b border-[var(--line)]">
            <strong>外部进程插件（只读展示）</strong>
            <span className="muted text-xs">为防止 Web 端任意命令执行，可执行路径仅支持通过本机 CLI（mt config import）配置</span>
          </div>
          <div className="p-4 text-xs mono text-[var(--ink-2)]">
            {cfg.plugins.map((pl, idx) => (
              <div key={idx}>
                {pl.dir} → [{pl.command.join(' ')}] {pl.disabled ? '(已停用)' : ''}
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}
