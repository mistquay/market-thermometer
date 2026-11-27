import { useCallback, useEffect, useMemo, useState } from 'react'
import { ExternalLink, NotebookPen, Plus, Search, Trash2 } from 'lucide-react'
import { api, type ResearchNote } from './api'

type Category = 'all' | 'security' | 'pm' | 'portfolio' | 'general'

function targetCategory(target: string): Exclude<Category, 'all'> {
  if (target.startsWith('stock:') || target.startsWith('fund:') || target.startsWith('ofund:')) return 'security'
  if (target.startsWith('pm:')) return 'pm'
  if (target.startsWith('portfolio:')) return 'portfolio'
  return 'general'
}

function targetLink(target: string): { href: string; label: string } | null {
  if (target.startsWith('stock:')) return { href: `#/quote/stock/${target.slice(6)}`, label: `个股 ${target.slice(6)}` }
  if (target.startsWith('fund:')) return { href: `#/quote/fund/${target.slice(5)}`, label: `基金 ${target.slice(5)}` }
  if (target.startsWith('ofund:')) return { href: `#/quote/ofund/${target.slice(6)}`, label: `场外基金 ${target.slice(6)}` }
  if (target.startsWith('pm:')) return { href: `#/predict/${target.slice(3)}`, label: `PM · ${target.slice(3)}` }
  if (target.startsWith('portfolio:')) return { href: '#/portfolio', label: `组合 #${target.slice(10)}` }
  return null
}

export default function NotesPage() {
  const [notes, setNotes] = useState<ResearchNote[]>([])
  const [q, setQ] = useState('')
  const [cat, setCat] = useState<Category>('all')
  const [tag, setTag] = useState('')
  const [editing, setEditing] = useState<{ id: number; target: string; title: string; content: string; tags: string } | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    api.notes('', 300).then(setNotes, (e: Error) => setError(e.message))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const allTags = useMemo(() => {
    const map = new Map<string, number>()
    for (const n of notes) {
      if (!n.tags) continue
      for (const raw of n.tags.split(/[,，]/)) {
        const t = raw.trim()
        if (t) map.set(t, (map.get(t) ?? 0) + 1)
      }
    }
    return [...map.entries()].sort((a, b) => b[1] - a[1]).slice(0, 20)
  }, [notes])

  const filtered = useMemo(() => {
    const kw = q.trim().toLowerCase()
    return notes.filter((n) => {
      if (cat !== 'all' && targetCategory(n.target) !== cat) return false
      if (tag) {
        const tags = (n.tags || '')
          .split(/[,，]/)
          .map((x) => x.trim())
          .filter(Boolean)
        if (!tags.includes(tag)) return false
      }
      if (kw) {
        const hay = `${n.title}\n${n.content}\n${n.tags}\n${n.target}`.toLowerCase()
        if (!hay.includes(kw)) return false
      }
      return true
    })
  }, [notes, q, cat, tag])

  const save = () => {
    if (!editing || !editing.title.trim()) return setError('请填写标题')
    setError('')
    api
      .saveNote({
        id: editing.id,
        target: editing.target.trim() || 'general',
        title: editing.title.trim(),
        content: editing.content,
        tags: editing.tags.trim(),
      })
      .then(
        () => {
          setEditing(null)
          load()
        },
        (e: Error) => setError(e.message),
      )
  }

  const remove = (id: number) => {
    if (!confirm('确认删除这条研究笔记？')) return
    api.deleteNote(id).then(load, (e: Error) => setError(e.message))
  }

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>研究笔记</h1>
          <p className="sub">
            汇总全站绑定到个股、场内基金、组合、Polymarket 事件及通用复盘的投研记录，支持全文检索、标签过滤与一键回跳标的。
            <span className="mono muted"> · 共 {notes.length} 条</span>
          </p>
        </div>
        <div className="actions">
          <button
            className="btn btn-primary"
            onClick={() => setEditing({ id: 0, target: 'general', title: '', content: '', tags: '' })}
          >
            <Plus className="h-4 w-4" />
            新建笔记
          </button>
        </div>
      </div>

      {error && (
        <div className="error" role="alert">
          {error}
        </div>
      )}

      {editing && (
        <section className="panel flex flex-col gap-3 p-4">
          <div className="flex flex-wrap gap-2">
            <input
              className="h-8 flex-1 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-3 text-sm text-[var(--ink)]"
              placeholder="标题（如：2026Q4 市场温度校准与宽基配置思考）"
              value={editing.title}
              onChange={(e) => setEditing({ ...editing, title: e.target.value })}
            />
            <input
              className="h-8 w-44 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-3 text-xs mono text-[var(--ink)]"
              placeholder="绑定对象（如 stock:600519 或 general）"
              value={editing.target}
              onChange={(e) => setEditing({ ...editing, target: e.target.value })}
            />
            <input
              className="h-8 w-40 rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] px-3 text-xs text-[var(--ink)]"
              placeholder="标签（逗号分隔）"
              value={editing.tags}
              onChange={(e) => setEditing({ ...editing, tags: e.target.value })}
            />
          </div>
          <textarea
            rows={5}
            className="w-full rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] p-3 text-sm text-[var(--ink)]"
            placeholder="记录逻辑、假设、证伪条件或交易复盘…"
            value={editing.content}
            onChange={(e) => setEditing({ ...editing, content: e.target.value })}
          />
          <div className="flex justify-end gap-2">
            <button className="btn" onClick={() => setEditing(null)}>
              取消
            </button>
            <button className="btn btn-primary" onClick={save}>
              保存笔记
            </button>
          </div>
        </section>
      )}

      {/* 检索与过滤条 */}
      <section className="panel flex flex-col gap-3 p-4">
        <div className="flex flex-wrap items-center gap-3">
          <div className="relative min-w-[220px] flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 h-3.5 w-3.5 -translate-y-1/2 text-[var(--ink-3)]" />
            <input
              className="h-8.5 w-full rounded-lg border border-[var(--line-strong)] bg-[var(--bg-subtle)] pr-3 pl-9 text-sm text-[var(--ink)] focus:border-[var(--accent)] focus:outline-none"
              placeholder="搜索标题、正文、标签或标的代码…"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
          <div className="seg">
            {(
              [
                ['all', '全部'],
                ['security', '个股 / 基金'],
                ['pm', 'Polymarket'],
                ['portfolio', '组合'],
                ['general', '通用复盘'],
              ] as [Category, string][]
            ).map(([k, label]) => (
              <button key={k} className={cat === k ? 'on' : ''} onClick={() => setCat(k)}>
                {label}
              </button>
            ))}
          </div>
        </div>
        {allTags.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-xs text-[var(--ink-3)]">标签：</span>
            <button className={`btn btn-ghost text-xs ${tag === '' ? 'text-[var(--accent)] font-semibold' : ''}`} onClick={() => setTag('')}>
              全部
            </button>
            {allTags.map(([t, cnt]) => (
              <button
                key={t}
                className={`btn btn-ghost text-xs ${tag === t ? 'text-[var(--accent)] font-semibold' : ''}`}
                onClick={() => setTag(tag === t ? '' : t)}
              >
                #{t} <span className="mono text-[11px] text-[var(--ink-3)]">({cnt})</span>
              </button>
            ))}
          </div>
        )}
      </section>

      {/* 笔记列表 */}
      <section className="panel overflow-hidden">
        {filtered.length === 0 ? (
          <div className="empty">
            <NotebookPen className="mx-auto mb-2 h-6 w-6 text-[var(--ink-3)]" />
            {notes.length === 0 ? '还没有研究笔记，点击右上角“新建笔记”或前往行情/Polymarket 详情页记录。' : '没有符合条件的笔记。'}
          </div>
        ) : (
          <div className="divide-y divide-[var(--line)]">
            {filtered.map((n) => {
              const link = targetLink(n.target)
              return (
                <article key={n.id} className="px-5 py-4">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <strong className="text-[15px] text-[var(--ink)]">{n.title}</strong>
                      {link ? (
                        <a href={link.href} className="tag tag-cold inline-flex items-center gap-1 no-underline hover:underline">
                          {link.label}
                          <ExternalLink className="h-3 w-3" />
                        </a>
                      ) : (
                        <span className="tag tag-muted mono">{n.target}</span>
                      )}
                      {n.tags &&
                        n.tags
                          .split(/[,，]/)
                          .map((t) => t.trim())
                          .filter(Boolean)
                          .map((t) => (
                            <button
                              key={t}
                              type="button"
                              onClick={() => setTag(t)}
                              className="tag tag-muted cursor-pointer border-0"
                            >
                              #{t}
                            </button>
                          ))}
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="mono text-[11px] text-[var(--ink-3)]">
                        {new Date(n.updatedAt).toLocaleString('zh-CN', { hour12: false })}
                      </span>
                      <button
                        className="btn btn-ghost text-xs"
                        onClick={() => setEditing({ id: n.id, target: n.target, title: n.title, content: n.content, tags: n.tags })}
                      >
                        编辑
                      </button>
                      <button className="btn btn-ghost text-[var(--bad)]" onClick={() => remove(n.id)}>
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </div>
                  {n.content && <p className="mt-2.5 whitespace-pre-wrap text-xs leading-relaxed text-[var(--ink-2)]">{n.content}</p>}
                </article>
              )
            })}
          </div>
        )}
      </section>
    </div>
  )
}
