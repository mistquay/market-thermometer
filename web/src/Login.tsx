import { useState, type FormEvent } from 'react'
import { api } from './api'

export default function Login({ onDone }: { onDone: () => void }) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

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
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <div className="brand">
          <span className="brand-mark" aria-hidden />
          市场温度计
        </div>
        <label className="field">
          <span>管理员口令</span>
          <input
            type="password"
            name="password"
            autoComplete="current-password"
            autoFocus
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error && <div className="error" role="alert">{error}</div>}
        <button className="btn btn-primary" disabled={busy || password === ''}>
          {busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  )
}
