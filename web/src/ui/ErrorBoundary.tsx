import { Component, type ErrorInfo, type ReactNode } from 'react'
import { AlertTriangle, Home, RotateCcw } from 'lucide-react'

type Props = {
  resetKey: string
  children: ReactNode
}

type State = {
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('[ErrorBoundary]', error, info.componentStack)
  }

  componentDidUpdate(prevProps: Props) {
    if (prevProps.resetKey !== this.props.resetKey && this.state.error) {
      this.setState({ error: null })
    }
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <div className="panel mx-auto my-10 max-w-xl overflow-hidden">
        <div className="flex items-center gap-2.5 border-b border-[var(--line)] bg-[color-mix(in_srgb,var(--bad)_10%,transparent)] px-5 py-3.5 text-[var(--bad)]">
          <AlertTriangle className="h-4 w-4 shrink-0" />
          <strong className="text-sm font-semibold">页面渲染出现异常</strong>
        </div>
        <div className="space-y-4 p-5">
          <p className="m-0 text-xs leading-relaxed text-[var(--ink-2)]">
            当前页面模块遇到未预期错误，已隔离保护，不影响其他页面正常使用。
          </p>
          <pre className="log text-[var(--bad)]">{this.state.error.message || String(this.state.error)}</pre>
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" className="btn btn-primary" onClick={() => this.setState({ error: null })}>
              <RotateCcw className="h-3.5 w-3.5" />
              重试渲染
            </button>
            <a href="#/overview" className="btn" onClick={() => this.setState({ error: null })}>
              <Home className="h-3.5 w-3.5" />
              返回总览
            </a>
          </div>
        </div>
      </div>
    )
  }
}
