import { useEffect, useMemo, useRef, useState } from 'react'
import { ArrowRight, Search, X } from 'lucide-react'
import { api, type SecurityHit } from './api'

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

export default function CommandPalette({
  open,
  onClose,
  items,
}: {
  open: boolean
  onClose: () => void
  items: NavItem[]
}) {
  const [query, setQuery] = useState('')
  const [secHits, setSecHits] = useState<SecurityHit[]>([])
  const [active, setActive] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!open) return
    setQuery('')
    setSecHits([])
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

  const pageHits = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return items
    return items.filter(
      (x) =>
        x.label.toLowerCase().includes(q) ||
        x.path.toLowerCase().includes(q) ||
        x.group.toLowerCase().includes(q) ||
        (x.keywords?.toLowerCase().includes(q) ?? false),
    )
  }, [items, query])

  const combined = useMemo(() => {
    const list: { key: string; href: string; title: string; subtitle: string; badge?: string }[] = []
    for (const p of pageHits) {
      list.push({ key: `p:${p.path}`, href: `#/${p.path}`, title: p.label, subtitle: p.group })
    }
    for (const s of secHits) {
      list.push({
        key: `s:${s.kind}:${s.code}`,
        href: `#/quote/${s.kind}/${s.code}`,
        title: `${s.code} ${s.name || '（无名称）'}`,
        subtitle: KIND_LABEL[s.kind] ?? s.kind,
        badge: s.hasData ? undefined : '无本地数据',
      })
    }
    return list
  }, [pageHits, secHits])

  useEffect(() => {
    setActive(0)
  }, [query, secHits.length])

  if (!open) return null

  const go = (href: string) => {
    location.hash = href
    onClose()
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/55 px-4 pt-[12vh] backdrop-blur-xs"
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
                go(combined[active].href)
              }
            }}
            placeholder="跳转页面，或输入代码 / 名称搜索股票与基金…"
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

        <div className="max-h-80 overflow-y-auto p-2">
          {combined.length === 0 ? (
            <div className="py-10 text-center text-xs text-[var(--ink-2)]">没有找到匹配的页面或证券</div>
          ) : (
            <ul className="m-0 list-none space-y-0.5 p-0">
              {combined.map((item, idx) => {
                const isOn = idx === active
                return (
                  <li key={item.key}>
                    <button
                      type="button"
                      onMouseEnter={() => setActive(idx)}
                      onClick={() => go(item.href)}
                      className={`flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-sm transition-colors ${
                        isOn ? 'bg-[var(--accent-soft)] text-[var(--accent)]' : 'text-[var(--ink)] hover:bg-[var(--panel-2)]'
                      }`}
                    >
                      <div className="flex min-w-0 items-center gap-2.5">
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
          <span>↑↓ 选择 · Enter 跳转 · Esc 关闭</span>
          <span className="mono">Ctrl K</span>
        </div>
      </div>
    </div>
  )
}
