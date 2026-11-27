import { Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { ArrowDownRight, ArrowUpRight, History, RefreshCw } from 'lucide-react'
import { api, type CryptoState, type Kline } from './api'
import { cssVar } from './EChart'
import { useTheme } from './theme'
import { RENDERERS, useKlineRenderer, type KlineData } from './kline/kline'

const IV_TEXT: Record<string, string> = { '1m': '1 分', '5m': '5 分', '15m': '15 分', '1h': '1 小时', '4h': '4 小时', '1d': '日线' }
const MAS = [20, 60]

const pad = (n: number) => String(n).padStart(2, '0')
function label(ms: number, daily: boolean) {
  const d = new Date(ms)
  if (daily) return d.toISOString().slice(0, 10)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
const digitsFor = (p: number) => (p >= 100 ? 2 : p >= 1 ? 4 : 6)
const baseOf = (sym: string) => sym.replace(/(USDT|USDC|FDUSD|USD)$/, '')

function sma(xs: number[], n: number): (number | null)[] {
  let s = 0
  return xs.map((x, i) => {
    s += x
    if (i >= n) s -= xs[i - n]
    return i >= n - 1 ? s / n : null
  })
}

export default function Crypto() {
  const [state, setState] = useState<CryptoState | null>(null)
  const [symbol, setSymbol] = useState('')
  const [interval, setInterval_] = useState('1h')
  const [bars, setBars] = useState<Kline[]>([])
  const [more, setMore] = useState(false)
  const [price, setPrice] = useState<{ price: number; source: string } | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [renderer, setRenderer] = useKlineRenderer()
  const { theme } = useTheme()

  const loadState = useCallback(() => api.crypto().then(setState, (e: Error) => setError(e.message)), [])
  useEffect(() => {
    loadState()
  }, [loadState])
  useEffect(() => {
    if (state?.configured && !symbol) {
      setSymbol(state.symbols[0])
      if (!state.intervals.includes(interval)) setInterval_(state.intervals[state.intervals.length - 1])
    }
  }, [state, symbol, interval])

  const running = state?.configured && state.status.running
  useEffect(() => {
    if (!running) return
    const id = window.setInterval(loadState, 3000)
    return () => window.clearInterval(id)
  }, [running, loadState])

  useEffect(() => {
    if (!symbol) return
    setBars([])
    setError('')
    api.cryptoKlines(symbol, interval).then(
      (r) => {
        setBars(r.klines)
        setMore(r.more)
      },
      (e: Error) => setError(e.message),
    )
  }, [symbol, interval])

  useEffect(() => {
    if (!symbol) return
    setPrice(null)
    const get = () => api.cryptoPrice(symbol).then(setPrice, () => setPrice(null))
    get()
    const id = window.setInterval(get, 15000)
    return () => window.clearInterval(id)
  }, [symbol])

  const loadOlder = () => {
    if (bars.length === 0) return
    api.cryptoKlines(symbol, interval, bars[0].t).then(
      (r) => {
        setBars((b) => [...r.klines, ...b])
        setMore(r.more)
      },
      (e: Error) => setError(e.message),
    )
  }

  const kline = useMemo<KlineData | null>(() => {
    if (bars.length === 0) return null
    const daily = interval === '1d'
    const close = bars.map((b) => b.c)
    const colors = [cssVar('--t-hot-1'), cssVar('--t-cold-2')]
    return {
      dates: bars.map((b) => label(b.t, daily)),
      times: daily ? undefined : bars.map((b) => b.t / 1000),
      close,
      open: bars.map((b) => b.o),
      high: bars.map((b) => b.h),
      low: bars.map((b) => b.l),
      volume: bars.map((b) => b.v),
      lines: MAS.map((n, i) => ({ name: `MA${n}`, color: colors[i], data: sma(close, n) })),
      title: `${symbol} ${IV_TEXT[interval] ?? interval}`,
      digits: digitsFor(close[close.length - 1]),
      volUnit: baseOf(symbol),
    }
  }, [bars, interval, symbol, theme])

  const sync = () => {
    setBusy(true)
    api
      .syncCrypto()
      .then(loadState, (e: Error) => setError(e.message))
      .finally(() => setBusy(false))
  }

  if (state && !state.configured) {
    return (
      <div className="page">
        <div className="page-head">
          <div>
            <h1>加密行情</h1>
          </div>
        </div>
        <div className="panel empty">未配置加密行情：在 config.yaml 的 crypto 段填写 sources 与 symbols 后重启。</div>
      </div>
    )
  }

  const last = bars[bars.length - 1]
  const prev = bars[bars.length - 2]
  const chg = last && prev ? last.c / prev.c - 1 : null
  const cov = state?.configured ? state.coverage.filter((c) => c.symbol === symbol) : []

  return (
    <div className="page">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="m-0">加密行情</h1>
          <p className="sub mt-1">
            7×24 本地 K 线（币安主源 / OKX 备用），本机时区显示；日线按 UTC 0 点切分。Polymarket 涨跌盘按 Chainlink 结算，与币安现价存在微小基差。
          </p>
        </div>
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      {state?.configured && (
        <section className="panel overflow-hidden">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-4 py-3">
            <div className="flex flex-wrap items-center gap-3">
              <div className="seg">
                {state.symbols.map((s) => (
                  <button key={s} className={s === symbol ? 'on' : ''} onClick={() => setSymbol(s)}>
                    {baseOf(s)}
                  </button>
                ))}
              </div>
              <div className="seg">
                {state.intervals.map((iv) => (
                  <button key={iv} className={iv === interval ? 'on' : ''} onClick={() => setInterval_(iv)}>
                    {IV_TEXT[iv] ?? iv}
                  </button>
                ))}
              </div>

              {price && (
                <div className="flex items-center gap-2.5 border-l border-[var(--line)] pl-3">
                  <span className="relative flex h-2 w-2" title="实时报价每 15 秒刷新">
                    <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[var(--ok)] opacity-60" />
                    <span className="relative inline-flex h-2 w-2 rounded-full bg-[var(--ok)]" />
                  </span>
                  <span className="text-lg font-bold tracking-tight mono text-[var(--ink)]">
                    ${price.price.toFixed(digitsFor(price.price))}
                  </span>
                  {chg !== null && (
                    <span
                      className={`inline-flex items-center gap-0.5 rounded-full px-2 py-0.5 text-xs font-semibold mono ${
                        chg >= 0
                          ? 'bg-[color-mix(in_srgb,#ef4444_14%,transparent)] text-[#ef4444]'
                          : 'bg-[color-mix(in_srgb,#10b981_14%,transparent)] text-[#10b981]'
                      }`}
                    >
                      {chg >= 0 ? <ArrowUpRight className="h-3.5 w-3.5" /> : <ArrowDownRight className="h-3.5 w-3.5" />}
                      {(chg * 100).toFixed(2)}%
                    </span>
                  )}
                  <span className="text-[11px] text-[var(--ink-3)] uppercase">{price.source}</span>
                </div>
              )}
            </div>

            <div className="flex items-center gap-2">
              {more && (
                <button className="btn btn-ghost" onClick={loadOlder}>
                  <History className="h-3.5 w-3.5" />
                  加载更早
                </button>
              )}
              <div className="seg" title="图表组件（记在本浏览器）">
                {RENDERERS.map((r) => (
                  <button key={r.id} className={r.id === renderer.id ? 'on' : ''} onClick={() => setRenderer(r.id)}>
                    {r.label}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="p-2">
            {kline ? (
              <Suspense fallback={<div className="empty" style={{ height: 550 }}>加载图表…</div>}>
                <renderer.Component data={kline} height={550} />
              </Suspense>
            ) : (
              <div className="empty" style={{ height: 550 }}>
                {symbol ? '本地还没有这个周期的 K 线，等待同步完成。' : '加载中…'}
              </div>
            )}
          </div>
        </section>
      )}

      {state?.configured && (
        <section className="panel overflow-hidden">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] px-5 py-3.5">
            <div className="flex flex-wrap items-center gap-2.5">
              <strong className="text-sm font-semibold">本地同步状态</strong>
              <span className={`tag ${state.status.running ? 'tag-cold' : 'tag-ok'}`}>
                {state.status.running ? '同步中' : `每 ${state.status.every} 轮询`}
              </span>
              {state.status.lastRun && (
                <span className="text-xs text-[var(--ink-2)] mono">
                  上次 {new Date(state.status.lastRun).toLocaleString('zh-CN')}
                </span>
              )}
              {state.status.errors > 0 && <span className="tag tag-bad">{state.status.errors} 项失败</span>}
            </div>
            <button className="btn" onClick={sync} disabled={busy || state.status.running}>
              <RefreshCw className={`h-3.5 w-3.5 ${state.status.running ? 'animate-spin' : ''}`} />
              {state.status.running ? '同步中…' : '立即同步'}
            </button>
          </div>
          <div className="table-scroll">
            <table className="table static">
              <thead>
                <tr>
                  <th>品种</th>
                  <th>周期</th>
                  <th className="num">K 线根数</th>
                  <th>最早时间</th>
                  <th>最新时间</th>
                  <th>数据源</th>
                </tr>
              </thead>
              <tbody>
                {cov.map((c) => (
                  <tr key={c.interval}>
                    <td className="font-medium">{c.symbol}</td>
                    <td>{IV_TEXT[c.interval] ?? c.interval}</td>
                    <td className="num mono">{c.count.toLocaleString()}</td>
                    <td className="mono text-xs text-[var(--ink-2)]">{label(c.first, false)}</td>
                    <td className="mono text-xs text-[var(--ink-2)]">{label(c.last, false)}</td>
                    <td>
                      <span className="tag tag-muted">{c.source}</span>
                    </td>
                  </tr>
                ))}
                {cov.length === 0 && (
                  <tr>
                    <td colSpan={6} className="muted">
                      本地还没有 {symbol} 的数据。
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
          {state.status.log.length > 0 && (
            <div className="p-4 pt-2">
              <pre className="log">{state.status.log.join('\n')}</pre>
            </div>
          )}
        </section>
      )}
    </div>
  )
}
