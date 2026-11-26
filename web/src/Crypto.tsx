import { Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { api, type CryptoState, type Kline } from './api'
import { cssVar } from './EChart'
import { RENDERERS, useKlineRenderer, type KlineData } from './kline/kline'

const IV_TEXT: Record<string, string> = { '1m': '1 分', '5m': '5 分', '15m': '15 分', '1h': '1 小时', '4h': '4 小时', '1d': '日线' }
const MAS = [20, 60]

const pad = (n: number) => String(n).padStart(2, '0')
/** 本地时间文字；日线按 UTC 日期（币安日线以 UTC 0 点切分）。 */
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

  const loadState = useCallback(() => api.crypto().then(setState, (e: Error) => setError(e.message)), [])
  useEffect(() => { loadState() }, [loadState])
  useEffect(() => {
    if (state?.configured && !symbol) {
      setSymbol(state.symbols[0])
      if (!state.intervals.includes(interval)) setInterval_(state.intervals[state.intervals.length - 1])
    }
  }, [state, symbol, interval])

  // 同步进行中时每 3 秒刷新状态
  const running = state?.configured && state.status.running
  useEffect(() => {
    if (!running) return
    const id = window.setInterval(loadState, 3000)
    return () => window.clearInterval(id)
  }, [running, loadState])

  useEffect(() => {
    if (!symbol) return
    setBars([]); setError('')
    api.cryptoKlines(symbol, interval).then((r) => { setBars(r.klines); setMore(r.more) }, (e: Error) => setError(e.message))
  }, [symbol, interval])

  // 实时价：每 15 秒
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
    api.cryptoKlines(symbol, interval, bars[0].t).then((r) => { setBars((b) => [...r.klines, ...b]); setMore(r.more) }, (e: Error) => setError(e.message))
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
      open: bars.map((b) => b.o), high: bars.map((b) => b.h), low: bars.map((b) => b.l),
      volume: bars.map((b) => b.v),
      lines: MAS.map((n, i) => ({ name: `MA${n}`, color: colors[i], data: sma(close, n) })),
      title: `${symbol} ${IV_TEXT[interval] ?? interval}`,
      digits: digitsFor(close[close.length - 1]),
      volUnit: baseOf(symbol),
    }
  }, [bars, interval, symbol])

  const sync = () => {
    setBusy(true)
    api.syncCrypto().then(loadState, (e: Error) => setError(e.message)).finally(() => setBusy(false))
  }

  if (state && !state.configured) {
    return (
      <div className="page">
        <div className="page-head"><div><h1>加密</h1></div></div>
        <div className="panel empty">未配置加密行情：在 config.yaml 的 crypto 段填写 sources 与 symbols 后重启。</div>
      </div>
    )
  }
  const last = bars[bars.length - 1]
  const prev = bars[bars.length - 2]
  const cov = state?.configured ? state.coverage.filter((c) => c.symbol === symbol) : []

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>加密</h1>
          <p className="sub">本地 K 线（7×24 定时从币安同步，OKX 备用），时间为本机时区；日线按 UTC 0 点切分。Polymarket 涨跌盘按 Chainlink 价结算，与币安价略有差异。</p>
        </div>
      </div>
      {error && <div className="error" role="alert">{error}</div>}
      {state?.configured && (
        <section className="panel chart-panel">
          <div className="panel-head">
            <div className="seg">
              {state.symbols.map((s) => <button key={s} className={s === symbol ? 'on' : ''} onClick={() => setSymbol(s)}>{baseOf(s)}</button>)}
            </div>
            <div className="seg">
              {state.intervals.map((iv) => <button key={iv} className={iv === interval ? 'on' : ''} onClick={() => setInterval_(iv)}>{IV_TEXT[iv] ?? iv}</button>)}
            </div>
            {price && (
              <span className="muted">
                实时 <strong className="mono">{price.price.toFixed(digitsFor(price.price))}</strong>（{price.source}）
                {last && prev && <> · 本周期 <span className={last.c >= prev.c ? 'up' : 'down'}>{((last.c / prev.c - 1) * 100).toFixed(2)}%</span></>}
              </span>
            )}
            <span style={{ flex: 1 }} />
            {more && <button className="btn btn-ghost" onClick={loadOlder}>加载更早</button>}
            <div className="seg" title="图表组件（记在本浏览器）">
              {RENDERERS.map((r) => <button key={r.id} className={r.id === renderer.id ? 'on' : ''} onClick={() => setRenderer(r.id)}>{r.label}</button>)}
            </div>
          </div>
          {kline ? (
            <Suspense fallback={<div className="empty" style={{ height: 520 }}>加载图表…</div>}>
              <renderer.Component data={kline} height={520} />
            </Suspense>
          ) : <div className="empty" style={{ height: 520 }}>{symbol ? '本地还没有这个周期的 K 线，等同步完成。' : '加载中…'}</div>}
        </section>
      )}
      {state?.configured && (
        <section className="panel">
          <div className="panel-head">
            <strong>同步</strong>
            <span className="muted">
              每 {state.status.every} 一轮
              {state.status.lastRun && <> · 上次 {new Date(state.status.lastRun).toLocaleString('zh-CN')}</>}
              {state.status.errors > 0 && <> · <span className="bad-text">{state.status.errors} 项失败</span></>}
            </span>
            <span style={{ flex: 1 }} />
            <button className="btn" onClick={sync} disabled={busy || state.status.running}>{state.status.running ? '同步中…' : '立即同步'}</button>
          </div>
          <div className="table-scroll">
            <table className="table static">
              <thead><tr><th>品种</th><th>周期</th><th className="num">根数</th><th>最早</th><th>最新</th><th>来源</th></tr></thead>
              <tbody>
                {cov.map((c) => (
                  <tr key={c.interval}>
                    <td>{c.symbol}</td><td>{IV_TEXT[c.interval] ?? c.interval}</td><td className="num mono">{c.count.toLocaleString()}</td>
                    <td className="mono">{label(c.first, false)}</td><td className="mono">{label(c.last, false)}</td><td>{c.source}</td>
                  </tr>
                ))}
                {cov.length === 0 && <tr><td colSpan={6} className="muted">本地还没有 {symbol} 的数据。</td></tr>}
              </tbody>
            </table>
          </div>
          {state.status.log.length > 0 && <pre className="log" style={{ margin: '0 16px 16px' }}>{state.status.log.join('\n')}</pre>}
        </section>
      )}
    </div>
  )
}
