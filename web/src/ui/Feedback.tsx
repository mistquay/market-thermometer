import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from 'lucide-react'

export type ToastKind = 'success' | 'error' | 'warn' | 'info'

type ToastItem = {
  id: number
  kind: ToastKind
  message: string
}

export type ConfirmOptions = {
  title?: string
  message: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}

type ToastApi = {
  show: (kind: ToastKind, message: string) => void
  success: (message: string) => void
  error: (message: string) => void
  warn: (message: string) => void
  info: (message: string) => void
}

type ConfirmFn = (opts: string | ConfirmOptions) => Promise<boolean>

type FeedbackCtx = {
  toast: ToastApi
  confirm: ConfirmFn
}

const noopToast: ToastApi = {
  show: () => {},
  success: () => {},
  error: () => {},
  warn: () => {},
  info: () => {},
}

const Ctx = createContext<FeedbackCtx>({
  toast: noopToast,
  confirm: () => Promise.resolve(false),
})

export function FeedbackProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([])
  const [dialog, setDialog] = useState<(ConfirmOptions & { resolve: (v: boolean) => void }) | null>(null)
  const nextId = useRef(1)
  const confirmBtnRef = useRef<HTMLButtonElement>(null)

  const dismiss = useCallback((id: number) => {
    setToasts((cur) => cur.filter((t) => t.id !== id))
  }, [])

  const show = useCallback(
    (kind: ToastKind, message: string) => {
      if (!message) return
      const id = nextId.current++
      setToasts((cur) => [...cur.slice(-3), { id, kind, message }])
      window.setTimeout(() => dismiss(id), kind === 'error' ? 5200 : 3400)
    },
    [dismiss],
  )

  const toast = useRef<ToastApi>({
    show,
    success: (m) => show('success', m),
    error: (m) => show('error', m),
    warn: (m) => show('warn', m),
    info: (m) => show('info', m),
  })
  toast.current.show = show
  toast.current.success = (m) => show('success', m)
  toast.current.error = (m) => show('error', m)
  toast.current.warn = (m) => show('warn', m)
  toast.current.info = (m) => show('info', m)

  const confirm = useCallback<ConfirmFn>((opts) => {
    const cfg: ConfirmOptions = typeof opts === 'string' ? { message: opts } : opts
    return new Promise<boolean>((resolve) => {
      setDialog({ ...cfg, resolve })
    })
  }, [])

  const closeDialog = useCallback(
    (ok: boolean) => {
      setDialog((cur) => {
        cur?.resolve(ok)
        return null
      })
    },
    [],
  )

  useEffect(() => {
    if (!dialog) return
    const id = requestAnimationFrame(() => confirmBtnRef.current?.focus())
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        closeDialog(false)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      cancelAnimationFrame(id)
      window.removeEventListener('keydown', onKey)
    }
  }, [dialog, closeDialog])

  return (
    <Ctx.Provider value={{ toast: toast.current, confirm }}>
      {children}

      {/* 右上角 Toast 轻提示堆叠 */}
      {toasts.length > 0 && (
        <div
          className="pointer-events-none fixed top-16 right-4 z-[60] flex w-full max-w-sm flex-col gap-2"
          aria-live="polite"
        >
          {toasts.map((t) => {
            const meta = {
              success: { Icon: CheckCircle2, cls: 'border-[color-mix(in_srgb,var(--ok)_35%,var(--line))] text-[var(--ok)]' },
              error: { Icon: XCircle, cls: 'border-[color-mix(in_srgb,var(--bad)_35%,var(--line))] text-[var(--bad)]' },
              warn: { Icon: AlertTriangle, cls: 'border-[color-mix(in_srgb,var(--warn)_35%,var(--line))] text-[var(--warn)]' },
              info: { Icon: Info, cls: 'border-[color-mix(in_srgb,var(--accent)_35%,var(--line))] text-[var(--accent)]' },
            }[t.kind]
            const Icon = meta.Icon
            return (
              <div
                key={t.id}
                role={t.kind === 'error' ? 'alert' : 'status'}
                className={`pointer-events-auto flex items-start gap-2.5 rounded-xl border bg-[var(--panel)]/95 px-3.5 py-2.5 text-xs shadow-[var(--shadow-md)] backdrop-blur-md ${meta.cls}`}
              >
                <Icon className="mt-0.5 h-4 w-4 shrink-0" />
                <span className="min-w-0 flex-1 leading-relaxed text-[var(--ink)]">{t.message}</span>
                <button
                  type="button"
                  onClick={() => dismiss(t.id)}
                  className="shrink-0 rounded p-0.5 text-[var(--ink-3)] hover:text-[var(--ink)]"
                  aria-label="关闭提示"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
            )
          })}
        </div>
      )}

      {/* 居中模态确认框 */}
      {dialog && (
        <div
          className="fixed inset-0 z-[70] flex items-center justify-center bg-black/55 px-4 backdrop-blur-xs"
          onClick={() => closeDialog(false)}
          role="alertdialog"
          aria-modal="true"
          aria-labelledby="mt-confirm-title"
          aria-describedby="mt-confirm-desc"
        >
          <div
            className="w-full max-w-md overflow-hidden rounded-2xl border border-[var(--line-strong)] bg-[var(--panel)] shadow-[var(--shadow-md)]"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-start gap-3 px-5 pt-5 pb-4">
              <span
                className={`mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl ${
                  dialog.danger
                    ? 'bg-[color-mix(in_srgb,var(--bad)_14%,transparent)] text-[var(--bad)]'
                    : 'bg-[color-mix(in_srgb,var(--accent)_14%,transparent)] text-[var(--accent)]'
                }`}
              >
                <AlertTriangle className="h-4.5 w-4.5" />
              </span>
              <div className="min-w-0 flex-1">
                <h2 id="mt-confirm-title" className="m-0 text-sm font-semibold text-[var(--ink)]">
                  {dialog.title || (dialog.danger ? '高风险操作确认' : '请确认操作')}
                </h2>
                <p id="mt-confirm-desc" className="mt-1.5 mb-0 whitespace-pre-line text-xs leading-relaxed text-[var(--ink-2)]">
                  {dialog.message}
                </p>
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 border-t border-[var(--line)] bg-[var(--bg-subtle)] px-5 py-3">
              <button type="button" className="btn" onClick={() => closeDialog(false)}>
                {dialog.cancelText || '取消'}
              </button>
              <button
                ref={confirmBtnRef}
                type="button"
                className={
                  dialog.danger
                    ? 'btn border-[var(--bad)] bg-[var(--bad)] text-white hover:opacity-90'
                    : 'btn btn-primary'
                }
                onClick={() => closeDialog(true)}
              >
                {dialog.confirmText || '确认'}
              </button>
            </div>
          </div>
        </div>
      )}
    </Ctx.Provider>
  )
}

export function useToast() {
  return useContext(Ctx).toast
}

export function useConfirm() {
  return useContext(Ctx).confirm
}
