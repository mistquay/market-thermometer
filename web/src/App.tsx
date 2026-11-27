import { lazy, Suspense, useEffect, useState, type ComponentType, type ReactNode } from 'react'
import {
  Activity,
  Blocks,
  CandlestickChart,
  ChevronRight,
  Coins,
  Database,
  Filter,
  FlaskConical,
  Gauge,
  Layers,
  LogOut,
  Menu,
  Moon,
  NotebookPen,
  PanelLeftClose,
  PanelLeftOpen,
  PieChart,
  Radio,
  Search,
  Settings as SettingsIcon,
  ShieldCheck,
  Sun,
  TrendingUp,
  Wallet,
  X,
} from 'lucide-react'
import { api, setUnauthorizedHandler } from './api'
import { useTheme } from './theme'
import CommandPalette from './CommandPalette'
import Login from './Login'

const Overview = lazy(() => import('./Overview'))
const History = lazy(() => import('./History'))
const DataPage = lazy(() => import('./DataPage'))
const Signals = lazy(() => import('./Signals'))
const Backtest = lazy(() => import('./Backtest'))
const Funds = lazy(() => import('./Funds'))
const Settings = lazy(() => import('./Settings'))
const Quality = lazy(() => import('./Quality'))
const Plugins = lazy(() => import('./Plugins'))
const Portfolio = lazy(() => import('./Portfolio'))
const Quote = lazy(() => import('./Quote'))
const Screener = lazy(() => import('./Screener'))
const Sim = lazy(() => import('./Sim'))
const NotesPage = lazy(() => import('./NotesPage'))
const Crypto = lazy(() => import('./Crypto'))
const Predict = lazy(() => import('./Predict'))

type Auth = 'checking' | 'in' | 'out'

type PageDef = {
  path: string
  label: string
  group: string
  icon: ComponentType<{ className?: string }>
  wide?: boolean
  keywords?: string
  el: () => ReactNode
}

const GROUPS = ['A 股温度计', '投研与模拟', '加密与预测', '数据与系统'] as const

const PAGES: PageDef[] = [
  { path: 'overview', label: '总览', group: 'A 股温度计', icon: Gauge, keywords: 'temperature dashboard 温度计', el: () => <Overview /> },
  { path: 'history', label: '历史走势', group: 'A 股温度计', icon: Activity, keywords: 'history chart', el: () => <History /> },
  { path: 'signals', label: '择时信号', group: 'A 股温度计', icon: Radio, keywords: 'signals events', el: () => <Signals /> },
  { path: 'backtest', label: '策略回测', group: 'A 股温度计', icon: FlaskConical, wide: true, keywords: 'backtest robust ablation 稳健性 消融', el: () => <Backtest /> },

  { path: 'quote', label: '行情图表', group: '投研与模拟', icon: CandlestickChart, wide: true, keywords: 'kline stock 股票', el: () => <Quote /> },
  { path: 'screener', label: '本地选股器', group: '投研与模拟', icon: Filter, wide: true, keywords: 'screener filter 选股 估值 pe pb ma200', el: () => <Screener /> },
  { path: 'portfolio', label: '组合分析', group: '投研与模拟', icon: PieChart, keywords: 'portfolio', el: () => <Portfolio /> },
  { path: 'funds', label: '基金自选', group: '投研与模拟', icon: Layers, keywords: 'funds etf', el: () => <Funds /> },
  { path: 'sim', label: 'A 股模拟盘', group: '投研与模拟', icon: Wallet, wide: true, keywords: 'paper trading 模拟交易', el: () => <Sim /> },
  { path: 'notes', label: '研究笔记', group: '投研与模拟', icon: NotebookPen, keywords: 'notes research journal 笔记 复盘', el: () => <NotesPage /> },

  { path: 'crypto', label: '加密行情', group: '加密与预测', icon: Coins, wide: true, keywords: 'btc eth binance okx', el: () => <Crypto /> },
  { path: 'predict', label: 'Polymarket', group: '加密与预测', icon: TrendingUp, wide: true, keywords: 'prediction market 预测 盘口 套利 聪明钱', el: () => <Predict /> },

  { path: 'data', label: '数据同步', group: '数据与系统', icon: Database, keywords: 'sync metrics', el: () => <DataPage /> },
  { path: 'quality', label: '数据质量', group: '数据与系统', icon: ShieldCheck, keywords: 'quality check gaps', el: () => <Quality /> },
  { path: 'plugins', label: '插件管理', group: '数据与系统', icon: Blocks, keywords: 'plugins rpc', el: () => <Plugins /> },
  { path: 'settings', label: '系统设置', group: '数据与系统', icon: SettingsIcon, keywords: 'notify push 推送', el: () => <Settings /> },
]

function currentPath() {
  const p = location.hash.replace(/^#\/?/, '').split('/')[0]
  return PAGES.some((x) => x.path === p) ? p : PAGES[0].path
}

function initialCollapsed() {
  try {
    return localStorage.getItem('mt-sidebar-collapsed') === '1'
  } catch {
    return false
  }
}

export default function App() {
  const [auth, setAuth] = useState<Auth>('checking')
  const [path, setPath] = useState(currentPath)
  const [collapsed, setCollapsed] = useState(initialCollapsed)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [cmdOpen, setCmdOpen] = useState(false)
  const { theme, toggleTheme } = useTheme()

  useEffect(() => {
    setUnauthorizedHandler(() => setAuth('out'))
    api.me().then(
      () => setAuth('in'),
      () => setAuth('out'),
    )
    const onHash = () => {
      setPath(currentPath())
      setMobileOpen(false)
    }
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setCmdOpen((v) => !v)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const toggleCollapse = () => {
    setCollapsed((v) => {
      const next = !v
      try {
        localStorage.setItem('mt-sidebar-collapsed', next ? '1' : '0')
      } catch {
        // ignore
      }
      return next
    })
  }

  if (auth === 'checking') {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--bg)]">
        <div className="h-6 w-6 animate-spin rounded-full border-2 border-[var(--line-strong)] border-t-[var(--accent)]" />
      </div>
    )
  }
  if (auth === 'out') return <Login onDone={() => setAuth('in')} />

  const page = PAGES.find((x) => x.path === path) ?? PAGES[0]

  const renderNav = (compact: boolean) => (
    <div className="flex flex-1 flex-col gap-5 overflow-y-auto px-3 py-4">
      {GROUPS.map((group) => {
        const items = PAGES.filter((p) => p.group === group)
        return (
          <div key={group} className="flex flex-col gap-1">
            {!compact && (
              <div className="px-2.5 pb-1 text-[11px] font-semibold tracking-wider text-[var(--ink-3)] uppercase">
                {group}
              </div>
            )}
            {items.map((item) => {
              const Icon = item.icon
              const active = item.path === path
              return (
                <a
                  key={item.path}
                  href={`#/${item.path}`}
                  title={compact ? `${item.group} · ${item.label}` : undefined}
                  aria-current={active ? 'page' : undefined}
                  className={`group flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-[13.5px] font-medium no-underline transition-colors ${
                    compact ? 'justify-center px-0' : ''
                  } ${
                    active
                      ? 'bg-[var(--accent-soft)] text-[var(--accent)]'
                      : 'text-[var(--ink-2)] hover:bg-[var(--panel-2)] hover:text-[var(--ink)]'
                  }`}
                >
                  <Icon
                    className={`h-4 w-4 shrink-0 transition-colors ${
                      active ? 'text-[var(--accent)]' : 'text-[var(--ink-3)] group-hover:text-[var(--ink)]'
                    }`}
                  />
                  {!compact && <span className="truncate">{item.label}</span>}
                </a>
              )
            })}
          </div>
        )
      })}
    </div>
  )

  return (
    <div className="flex min-h-screen bg-[var(--bg)] text-[var(--ink)]">
      {/* 桌面端左侧栏 */}
      <aside
        className={`sticky top-0 hidden h-screen shrink-0 flex-col border-r border-[var(--line)] bg-[var(--bg-subtle)] transition-[width] duration-200 lg:flex ${
          collapsed ? 'w-16' : 'w-56'
        }`}
      >
        <div className={`flex h-14 items-center border-b border-[var(--line)] ${collapsed ? 'justify-center px-2' : 'justify-between px-4'}`}>
          <a href="#/overview" className="flex items-center gap-2.5 text-[var(--ink)] no-underline">
            <span
              className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg shadow-sm"
              style={{ background: 'linear-gradient(135deg, var(--t-cold-2) 0%, var(--accent) 52%, var(--t-hot-2) 100%)' }}
              aria-hidden
            >
              <Gauge className="h-4 w-4 text-white" />
            </span>
            {!collapsed && (
              <div className="flex items-center gap-2">
                <span className="text-[14.5px] font-semibold tracking-tight">市场温度计</span>
                <span className="rounded border border-[var(--line-strong)] bg-[var(--panel-2)] px-1.5 py-0.5 text-[10px] font-medium text-[var(--ink-2)]">
                  终端
                </span>
              </div>
            )}
          </a>
        </div>

        {renderNav(collapsed)}

        <div className={`flex items-center border-t border-[var(--line)] p-2.5 ${collapsed ? 'flex-col gap-1.5' : 'justify-between'}`}>
          <button
            type="button"
            onClick={toggleCollapse}
            title={collapsed ? '展开侧边栏' : '收起侧边栏'}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--ink-2)] transition-colors hover:bg-[var(--panel-2)] hover:text-[var(--ink)]"
          >
            {collapsed ? <PanelLeftOpen className="h-4 w-4" /> : <PanelLeftClose className="h-4 w-4" />}
          </button>
          <button
            type="button"
            onClick={() => api.logout().finally(() => setAuth('out'))}
            title="退出登录"
            className={`flex h-8 items-center gap-1.5 rounded-lg text-xs font-medium text-[var(--ink-2)] transition-colors hover:bg-[var(--panel-2)] hover:text-[var(--bad)] ${
              collapsed ? 'w-8 justify-center' : 'px-2.5'
            }`}
          >
            <LogOut className="h-3.5 w-3.5" />
            {!collapsed && <span>退出</span>}
          </button>
        </div>
      </aside>

      {/* 移动端抽屉侧边栏 */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 flex lg:hidden">
          <div className="fixed inset-0 bg-black/55 backdrop-blur-xs" onClick={() => setMobileOpen(false)} />
          <aside className="relative z-10 flex h-full w-64 flex-col border-r border-[var(--line)] bg-[var(--bg-subtle)] shadow-2xl">
            <div className="flex h-14 items-center justify-between border-b border-[var(--line)] px-4">
              <div className="flex items-center gap-2.5">
                <span
                  className="flex h-7 w-7 items-center justify-center rounded-lg"
                  style={{ background: 'linear-gradient(135deg, var(--t-cold-2) 0%, var(--accent) 52%, var(--t-hot-2) 100%)' }}
                >
                  <Gauge className="h-4 w-4 text-white" />
                </span>
                <span className="font-semibold">市场温度计</span>
              </div>
              <button
                type="button"
                onClick={() => setMobileOpen(false)}
                className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--ink-2)] hover:bg-[var(--panel-2)]"
                aria-label="关闭菜单"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            {renderNav(false)}
            <div className="border-t border-[var(--line)] p-3">
              <button
                type="button"
                onClick={() => api.logout().finally(() => setAuth('out'))}
                className="flex w-full items-center justify-center gap-2 rounded-lg border border-[var(--line-strong)] py-2 text-xs font-medium text-[var(--ink-2)] hover:text-[var(--bad)]"
              >
                <LogOut className="h-3.5 w-3.5" />
                退出登录
              </button>
            </div>
          </aside>
        </div>
      )}

      {/* 右侧主区域 */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-4 border-b border-[var(--line)] bg-[var(--bg)]/85 px-4 backdrop-blur-md lg:px-6">
          <div className="flex min-w-0 items-center gap-2.5">
            <button
              type="button"
              onClick={() => setMobileOpen(true)}
              className="flex h-8 w-8 items-center justify-center rounded-lg text-[var(--ink-2)] hover:bg-[var(--panel-2)] hover:text-[var(--ink)] lg:hidden"
              aria-label="打开菜单"
            >
              <Menu className="h-4 w-4" />
            </button>
            <div className="flex min-w-0 items-center gap-1.5 text-sm">
              <span className="hidden text-[var(--ink-3)] sm:inline">{page.group}</span>
              <ChevronRight className="hidden h-3.5 w-3.5 text-[var(--ink-3)] sm:inline" />
              <span className="truncate font-semibold text-[var(--ink)]">{page.label}</span>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => setCmdOpen(true)}
              className="flex h-8 items-center gap-2 rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] px-2.5 text-xs text-[var(--ink-2)] transition-colors hover:border-[var(--ink-3)] hover:text-[var(--ink)] sm:w-56 sm:justify-between"
            >
              <span className="flex items-center gap-2">
                <Search className="h-3.5 w-3.5 text-[var(--ink-3)]" />
                <span className="hidden sm:inline">跳转页面 / 搜股票…</span>
              </span>
              <kbd className="mono hidden rounded border border-[var(--line)] bg-[var(--bg-subtle)] px-1.5 py-0.5 text-[10px] text-[var(--ink-3)] sm:inline">
                Ctrl K
              </kbd>
            </button>

            <button
              type="button"
              onClick={toggleTheme}
              title={theme === 'dark' ? '切换为清爽浅色' : '切换为暗色终端'}
              aria-label="切换明暗主题"
              className="flex h-8 w-8 items-center justify-center rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] text-[var(--ink-2)] transition-colors hover:border-[var(--ink-3)] hover:text-[var(--ink)]"
            >
              {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </button>
          </div>
        </header>

        <main className={`mx-auto w-full flex-1 px-4 py-5 lg:px-6 lg:py-6 ${page.wide ? 'max-w-[1680px]' : 'max-w-[1320px]'}`}>
          <Suspense
            fallback={
              <div className="flex min-h-[260px] items-center justify-center">
                <div className="h-6 w-6 animate-spin rounded-full border-2 border-[var(--line-strong)] border-t-[var(--accent)]" />
              </div>
            }
          >
            {page.el()}
          </Suspense>
        </main>
      </div>

      <CommandPalette open={cmdOpen} onClose={() => setCmdOpen(false)} items={PAGES} />
    </div>
  )
}
