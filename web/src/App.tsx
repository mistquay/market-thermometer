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

type Auth = 'checking' | 'in' | 'out'

const PAGES = [
  { path: 'overview', label: '总览', el: () => <Overview /> },
  { path: 'history', label: '历史', el: () => <History /> },
  { path: 'signals', label: '信号', el: () => <Signals /> },
  { path: 'backtest', label: '回测', el: () => <Backtest /> },
  { path: 'funds', label: '基金', el: () => <Funds /> },
  { path: 'data', label: '数据', el: () => <DataPage /> },
  { path: 'settings', label: '设置', el: () => <Settings /> },
]

function currentPath() {
  const p = location.hash.replace(/^#\/?/, '')
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
