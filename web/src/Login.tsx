import { useState, type FormEvent } from 'react'
import { Eye, EyeOff, Gauge, Lock, Moon, Sun } from 'lucide-react'
import { api } from './api'
import { useTheme } from './theme'

export default function Login({ onDone }: { onDone: () => void }) {
  const [password, setPassword] = useState('')
  const [showPwd, setShowPwd] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const { theme, toggleTheme } = useTheme()

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api.login(password)
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
      setPassword('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-[var(--bg)] px-4">
      {/* 柔和氛围背景光 */}
      <div
        className="pointer-events-none absolute -top-32 left-1/2 h-96 w-[38rem] -translate-x-1/2 rounded-full opacity-25 blur-3xl"
        style={{ background: 'radial-gradient(circle, var(--accent) 0%, transparent 70%)' }}
        aria-hidden
      />

      <button
        type="button"
        onClick={toggleTheme}
        title={theme === 'dark' ? '切换浅色模式' : '切换暗色模式'}
        className="fixed top-5 right-5 flex h-9 w-9 items-center justify-center rounded-lg border border-[var(--line-strong)] bg-[var(--panel)] text-[var(--ink-2)] transition-colors hover:text-[var(--ink)]"
      >
        {theme === 'dark' ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
      </button>

      <form
        className="relative z-10 flex w-full max-w-sm flex-col gap-5 rounded-2xl border border-[var(--line-strong)] bg-[var(--panel)] p-7 shadow-[var(--shadow-md)]"
        onSubmit={submit}
      >
        <div className="flex flex-col items-center gap-3 text-center">
          <span
            className="flex h-11 w-11 items-center justify-center rounded-xl shadow-md"
            style={{ background: 'linear-gradient(135deg, var(--t-cold-2) 0%, var(--accent) 52%, var(--t-hot-2) 100%)' }}
          >
            <Gauge className="h-6 w-6 text-white" />
          </span>
          <div>
            <h1 className="m-0 text-lg font-semibold tracking-tight text-[var(--ink)]">市场温度计</h1>
            <p className="mt-1 text-xs text-[var(--ink-2)]">多市场投研与模拟交易终端</p>
          </div>
        </div>

        <label className="field">
          <span className="flex items-center gap-1.5 text-xs font-medium text-[var(--ink-2)]">
            <Lock className="h-3.5 w-3.5" />
            管理员口令
          </span>
          <div className="relative">
            <input
              type={showPwd ? 'text' : 'password'}
              name="password"
              autoComplete="current-password"
              autoFocus
              placeholder="输入访问口令"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full pr-10"
            />
            <button
              type="button"
              onClick={() => setShowPwd((v) => !v)}
              className="absolute top-1/2 right-2.5 -translate-y-1/2 text-[var(--ink-3)] hover:text-[var(--ink)]"
              tabIndex={-1}
              aria-label={showPwd ? '隐藏口令' : '显示口令'}
            >
              {showPwd ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </button>
          </div>
        </label>

        {error && (
          <div className="error" role="alert">
            {error}
          </div>
        )}

        <button className="btn btn-primary h-10 w-full text-sm" disabled={busy || password === ''}>
          {busy ? '验证中…' : '进入终端'}
        </button>
      </form>
    </div>
  )
}
