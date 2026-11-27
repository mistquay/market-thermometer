import { useCallback, useEffect, useState } from 'react'
import { NotebookPen, Plus, Trash2 } from 'lucide-react'
import { api, type ResearchNote } from './api'
import { useConfirm, useToast } from './ui/Feedback'

/** 可复用的研究笔记卡片，可绑定到个股、基金、组合或 Polymarket 事件。 */
export function NotesBox({ target, label }: { target: string; label?: string }) {
  const [notes, setNotes] = useState<ResearchNote[]>([])
  const [editing, setEditing] = useState<{ id: number; title: string; content: string; tags: string } | null>(null)
  const [error, setError] = useState('')
  const confirm = useConfirm()
  const toast = useToast()

  const load = useCallback(() => {
    if (!target) return
    api.notes(target, 30).then(setNotes, (e: Error) => setError(e.message))
  }, [target])

  useEffect(() => {
    load()
  }, [load])

  const save = () => {
    if (!editing || !editing.title.trim()) return setError('请填写标题')
    setError('')
    api.saveNote({ id: editing.id, target, title: editing.title.trim(), content: editing.content, tags: editing.tags.trim() }).then(
      () => {
        setEditing(null)
        toast.success('研究笔记已保存')
        load()
      },
      (e: Error) => setError(e.message),
    )
  }

  const remove = async (id: number) => {
    const ok = await confirm({ title: '删除研究笔记', message: '确认删除这条研究笔记？删除后不可恢复。', confirmText: '删除', danger: true })
    if (!ok) return
    api.deleteNote(id).then(() => {
      toast.success('已删除笔记')
      load()
    }, (e: Error) => setError(e.message))
  }

  return (
    <section className="panel overflow-hidden">
      <div className="panel-head border-b border-[var(--line)]">
        <NotebookPen className="h-4 w-4 text-[var(--accent)]" />
        <strong>研究笔记{label ? ` · ${label}` : ''}</strong>
        <span className="muted small mono">{target}</span>
        <button
          className="btn btn-ghost"
          style={{ marginLeft: 'auto' }}
          onClick={() => setEditing({ id: 0, title: '', content: '', tags: '' })}
        >
          <Plus className="h-3.5 w-3.5" />
          记一条
        </button>
      </div>

      {error && (
        <div className="p-3 pb-0">
          <div className="error">{error}</div>
        </div>
      )}

      {editing && (
        <div className="flex flex-col gap-2.5 border-b border-[var(--line)] bg-[var(--bg-subtle)] p-4">
          <div className="flex flex-wrap gap-2">
            <input
              className="h-8 flex-1 rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] px-3 text-sm text-[var(--ink)]"
              placeholder="标题（如：2026Q4 估值复盘）"
              value={editing.title}
              onChange={(e) => setEditing({ ...editing, title: e.target.value })}
            />
            <input
              className="h-8 w-44 rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] px-3 text-xs text-[var(--ink)]"
              placeholder="标签（逗号分隔）"
              value={editing.tags}
              onChange={(e) => setEditing({ ...editing, tags: e.target.value })}
            />
          </div>
          <textarea
            rows={4}
            className="w-full rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] p-2.5 text-sm text-[var(--ink)]"
            placeholder="写下逻辑、观察点或复盘结论…"
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
        </div>
      )}

      {notes.length === 0 && !editing ? (
        <div className="empty">暂无研究笔记。</div>
      ) : (
        <div className="divide-y divide-[var(--line)]">
          {notes.map((n) => (
            <div key={n.id} className="px-5 py-3.5">
              <div className="flex items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-2">
                  <strong className="text-sm">{n.title}</strong>
                  {n.tags &&
                    n.tags
                      .split(/[,，]/)
                      .map((t) => t.trim())
                      .filter(Boolean)
                      .map((t) => (
                        <span key={t} className="tag tag-muted">
                          {t}
                        </span>
                      ))}
                </div>
                <div className="flex items-center gap-2">
                  <span className="mono text-[11px] text-[var(--ink-3)]">
                    {new Date(n.updatedAt).toLocaleString('zh-CN', { hour12: false })}
                  </span>
                  <button
                    className="btn btn-ghost text-xs"
                    onClick={() => setEditing({ id: n.id, title: n.title, content: n.content, tags: n.tags })}
                  >
                    编辑
                  </button>
                  <button className="btn btn-ghost text-[var(--bad)]" onClick={() => remove(n.id)}>
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              </div>
              {n.content && <p className="mt-2 whitespace-pre-wrap text-xs leading-relaxed text-[var(--ink-2)]">{n.content}</p>}
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
