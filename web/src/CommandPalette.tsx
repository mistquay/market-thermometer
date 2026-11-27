import { useEffect, useMemo, useRef, useState } from 'react'
import { ArrowRight, Clock, Moon, Search, Sparkles, Sun, X } from 'lucide-react'
import { api, type SecurityHit } from './api'
import type { RecentSec } from './Quote'

export type NavItem = {
  path: string
  label: string
  group: string
  keywords?: string
}

const KIND_LABEL: Record<string, string> = {
  stock: '股票',
  fund: '场内基金',
  ofund: '场外基金',
}

const RECENT_KEY = 'mt-recent-securities'

function loadRecent(): RecentSec[] {
  try {
    const raw = localStorage.getItem(RECENT_KEY)
    const parsed = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.slice(0, 5) : []
  } catch {
    return []
  }
}

type PaletteEntry = {
  key: string
  section: string
  title: string
  subtitle: string
  badge?: string
  kind?: 'action' | 'recent' | 'page' | 'sec'
  run: () => void
}

export default function CommandPalette({
  open,
  onClose,
  items,
  theme = 'dark',
  onToggleTheme,
}: {
  open: boolean
  onClose: () => void
  items: NavItem[]
  theme?: 'dark' | 'light'
  onToggleTheme?: () => void
}) {
  const [query, setQuery] = useState('')
  const [secHits, setSecHits] = useState<SecurityHit[]>([])
  const [recent, setRecent] = useState<RecentSec[]>([])
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!open) return
    setQuery('')
    setSecHits([])
    setRecent(loadRecent())
    setActive(0)
    const id = requestAnimationFrame(() => inputRef.current?.focus())
    return () => cancelAnimationFrame(id)
  }, [open])

  useEffect(() => {
    const q = query.trim()
    if (!open || q.length < 2) {
      setSecHits([])
      return
    }
    const t = setTimeout(() => {
      api.searchSecurities(q).then(
        (r) => setSecHits(r.items.slice(0, 8)),
        () => setSecHits([]),
      )
    }, 180)
    return () => clearTimeout(t)
  }, [open, query])

  const go = (href: string) => {
    location.hash = href
    onClose()
  }

  const combined = useMemo<PaletteEntry[]>(() => {
    const q = query.trim().toLowerCase()
    const list: PaletteEntry[] = []

    // 快捷动作
    const actions: { id: string; title: string; subtitle: string; kw: string; run: () => void }[] = [
      ...(onToggleTheme
        ? [
            {
              id: 'toggle-theme',
              title: theme === 'dark' ? '切换为清爽浅色主题' : '切换为暗色终端主题',
              subtitle: '快捷动作',
              kw: 'theme dark light 主题 浅色 暗色 亮色',
              run: () => {
                onToggleTheme()
                onClose()
              },
            },
          ]
        : []),
      {
        id: 'new-note',
        title: '前往研究笔记（查看/新建复盘）',
        subtitle: '快捷动作',
        kw: 'note research journal 笔记 复盘',
        run: () => go('#/notes'),
      },
      {
        id: 'go-screener',
        title: '打开本地选股器（5年 PE/PB 分位筛选）',
        subtitle: '快捷动作',
        kw: 'screener 选股 低估 pe pb',
        run: () => go('#/screener'),
      },
      {
        id: 'go-predict',
        title: '打开 Polymarket 信号回放与套利中心',
        subtitle: '快捷动作',
        kw: 'polymarket eval arb shadow 回放 影子 套利',
        run: () => go('#/predict'),
      },
    ]

    if (!q && recent.length > 0) {
      for (const r of recent) {
        list.push({
          key: `r:${r.kind}:${r.code}`,
          section: '最近访问标的',
          title: `${r.code} ${r.name}`,
          subtitle: KIND_LABEL[r.kind] ?? r.kind,
          kind: 'recent',
          run: () => go(`#/quote/${r.kind}/${r.code}`),
        })
      }
    }

    const matchedActions = q
      ? actions.filter((a) => a.title.toLowerCase().includes(q) || a.kw.toLowerCase().includes(q))
      : actions.slice(0, 2)
    for (const a of matchedActions) {
      list.push({
        key: `a:${a.id}`,
        section: '快捷动作',
        title: a.title,
        subtitle: a.subtitle,
        kind: 'action',
        run: a.run,
      })
    }

    const pageHits = !q
      ? items
      : items.filter(
          (x) =>
            x.label.toLowerCase().includes(q) ||
            x.path.toLowerCase().includes(q) ||
            x.group.toLowerCase().includes(q) ||
            (x.keywords?.toLowerCase().includes(q) ?? false),
        )
    for (const p of pageHits) {
      list.push({
        key: `p:${p.path}`,
        section: '功能页面',
        title: p.label,
        subtitle: p.group,
        kind: 'page',
        run: () => go(`#/${p.path}`),
      })
    }

    for (const s of secHits) {
      list.push({
        key: `s:${s.kind}:${s.code}`,
        section: '证券行情直达',
        title: `${s.code} ${s.name || '（无名称）'}`,
        subtitle: KIND_LABEL[s.kind] ?? s.kind,
        badge: s.hasData ? undefined : '无本地数据',
        kind: 'sec',
        run: () => go(`#/quote/${s.kind}/${s.code}`),
      })
    }
    return list
  }, [items, query, secHits, recent, theme, onToggleTheme])

  useEffect(() => {
    setActive(0)
  }, [query, secHits.length])

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/55 px-4 pt-[11vh] backdrop-blur-xs"
      onClick={onClose}
      role="dialog"
      aria-modal="true"
      aria-label="快速跳转与搜索"
    >
      <div
        className="w-full max-w-lg overflow-hidden rounded-xl border border-[var(--line-strong)] bg-[var(--panel)] shadow-[var(--shadow-md)]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-3 border-b border-[var(--line)] px-4 py-3">
          <Search className="h-4 w-4 shrink-0 text-[var(--ink-2)]" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.preventDefault()
                onClose()
              } else if (e.key === 'ArrowDown') {
                e.preventDefault()
                setActive((i) => (combined.length ? (i + 1) % combined.length : 0))
              } else if (e.key === 'ArrowUp') {
                e.preventDefault()
                setActive((i) => (combined.length ? (i - 1 + combined.length) % combined.length : 0))
              } else if (e.key === 'Enter' && combined[active]) {
                e.preventDefault()
                combined[active].run()
              }
            }}
            placeholder="跳转页面、执行快捷动作，或输入代码/名称搜股票…"
            className="w-full border-0 bg-transparent text-sm text-[var(--ink)] placeholder:text-[var(--ink-3)] focus:outline-none"
          />
          <button
            type="button"
            onClick={onClose}
            className="flex h-6 w-6 items-center justify-center rounded-md text-[var(--ink-2)] hover:bg-[var(--panel-2)] hover:text-[var(--ink)]"
            aria-label="关闭"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="max-h-96 overflow-y-auto p-2">
          {combined.length === 0 ? (
            <div className="py-10 text-center text-xs text-[var(--ink-2)]">没有找到匹配的页面、动作或证券</div>
          ) : (
            <ul className="m-0 list-none space-y-0.5 p-0">
              {combined.map((item, idx) => {
                const isOn = idx === active
                const showHead = idx === 0 || combined[idx - 1].section !== item.section
                return (
                  <li key={item.key}>
                    {showHead && (
                      <div className="px-3 pt-2 pb-1 text-[10.5px] font-semibold tracking-wider text-[var(--ink-3)] uppercase">
                        {item.section}
                      </div>
                    )}
                    <button
                      type="button"
                      onMouseEnter={() => setActive(idx)}
                      onClick={item.run}
                      className={`flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-sm transition-colors ${
                        isOn ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--ink)] hover:bg-[var(--panel-2)]'
                      }`}
                    >
                      <div className="flex min-w-0 items-center gap-2">
                        {item.kind === 'recent' && <Clock className="h-3.5 w-3.5 shrink-0 text-[var(--ink-3)]" />}
                        {item.kind === 'action' &&
                          (item.key === 'a:toggle-theme' ? (
                            theme === 'dark' ? (
                              <Sun className="h-3.5 w-3.5 shrink-0 text-[var(--warn)]" />
                            ) : (
                              <Moon className="h-3.5 w-3.5 shrink-0 text-[var(--accent)]" />
                            )
                          ) : (
                            <Sparkles className="h-3.5 w-3.5 shrink-0 text-[var(--accent)]" />
                          ))}
                        <span className="truncate font-medium">{item.title}</span>
                        {item.badge && <span className="tag tag-warn">{item.badge}</span>}
                      </div>
                      <div className="flex shrink-0 items-center gap-2 text-xs text-[var(--ink-2)]">
                        <span>{item.subtitle}</span>
                        {isOn && <ArrowRight className="h-3.5 w-3.5 text-[var(--accent)]" />}
                      </div>
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
        </div>

        <div className="flex items-center justify-between border-t border-[var(--line)] bg-[var(--bg-subtle)] px-4 py-2 text-[11px] text-[var(--ink-3)]">
          <span>↑↓ 选择 · Enter 执行 · Esc 关闭</span>
          <span className="mono">Ctrl K</span>
        </div>
      </div>
    </div>
  )
}
