import { useEffect, useState } from 'react'
import { api, setUnauthorizedHandler } from './api'
import Login from './Login'
import Overview from './Overview'
import History from './History'
import DataPage from './DataPage'
import Signals from './Signals'
import Backtest from './Backtest'
import Funds from './Funds'
import Settings from './Settings'
import Quality from './Quality'
import Plugins from './Plugins'
import Portfolio from './Portfolio'
import Quote from './Quote'
import Sim from './Sim'
import Crypto from './Crypto'
import Predict from './Predict'

type Auth = 'checking' | 'in' | 'out'

const PAGES = [
  { path: 'overview', label: '总览', el: () => <Overview /> },
  { path: 'history', label: '历史', el: () => <History /> },
  { path: 'signals', label: '信号', el: () => <Signals /> },
  { path: 'backtest', label: '回测', el: () => <Backtest /> },
  { path: 'portfolio', label: '组合', el: () => <Portfolio /> },
  { path: 'quote', label: '行情', el: () => <Quote /> },
  { path: 'sim', label: '模拟', el: () => <Sim /> },
  { path: 'crypto', label: '加密', el: () => <Crypto /> },
  { path: 'predict', label: '预测', el: () => <Predict /> },
  { path: 'funds', label: '基金', el: () => <Funds /> },
  { path: 'data', label: '数据', el: () => <DataPage /> },
  { path: 'quality', label: '质量', el: () => <Quality /> },
  { path: 'plugins', label: '插件', el: () => <Plugins /> },
  { path: 'settings', label: '设置', el: () => <Settings /> },
]

function currentPath() {
  const p = location.hash.replace(/^#\/?/, '').split('/')[0] // 允许带参数，如 #/quote/stock/600000
  return PAGES.some((x) => x.path === p) ? p : PAGES[0].path
}

export default function App() {
  const [auth, setAuth] = useState<Auth>('checking')
  const [path, setPath] = useState(currentPath)

  useEffect(() => {
    setUnauthorizedHandler(() => setAuth('out'))
    api.me().then(
      () => setAuth('in'),
      () => setAuth('out'),
    )
    const onHash = () => setPath(currentPath())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  if (auth === 'checking') return null
  if (auth === 'out') return <Login onDone={() => setAuth('in')} />

  const page = PAGES.find((x) => x.path === path)!
  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark" aria-hidden />
          市场温度计
          <span className="brand-market">A股</span>
        </div>
        <nav className="nav">
          {PAGES.map((p) => (
            <a
              key={p.path}
              href={`#/${p.path}`}
              className={`nav-item ${p.path === path ? 'active' : ''}`}
              aria-current={p.path === path ? 'page' : undefined}
            >
              {p.label}
            </a>
          ))}
        </nav>
        <button className="btn btn-ghost" onClick={() => api.logout().finally(() => setAuth('out'))}>
          退出
        </button>
      </header>
      <main className="main">{page.el()}</main>
    </div>
  )
}
