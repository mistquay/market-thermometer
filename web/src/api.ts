// 后端 JSON API 的类型与调用封装。

export type Coverage = {
  metric: string
  source: string
  first: string
  last: string
  count: number
  fetchedAt: string
}

export type Metric = {
  id: string
  market: string
  name: string
  unit: string
  lagDays: number
  note?: string
  route: string[]
  coverage: Coverage[] | null
}

export type Point = { date: string; value: number }
export type Series = { metric: string; source: string; points: Point[] }

export type JobStatus = {
  id: number
  kind: 'sync' | 'backfill' | 'breadth' | 'breadth-backfill'
  state: 'running' | 'done' | 'failed' | 'partial'
  total: number
  done: number
  current: string
  log: string[] | null
  startedAt: string
  finishedAt?: string
}

export type JobRun = {
  id: number
  job: string
  status: string
  detail: string
  startedAt: string
  finishedAt: string
}

export type Num = number | null

export type DayScore = {
  date: string
  close: Num
  erp: Num
  v: Num
  tier: '' | 'cheap' | 'fair' | 'expensive'
  heat: Num
  leverage: Num
  breadth: Num
  rsiD: Num
  rsiW: Num
  rMix: Num
  t: [Num, Num, Num, Num]
  tBase: Num
  tPct: Num
  tS: Num
  nLow: number
  nHigh: number
  degraded: boolean
}

export type Params = {
  cheapBelow: number
  expensiveAbove: number
  extreme: number
  weights: [number, number, number, number]
  [k: string]: unknown
}

export type SignalEvent = {
  date: string
  type: 'ARMED_BOTTOM' | 'ARMED_TOP' | 'BUY_DIP' | 'SELL_PEAK' | 'DISARMED' | 'VALUATION_TIER_CHANGE' | 'DATA_DEGRADED' | string
  reason: string
  observe: boolean
  close: Num
  tS: Num
  v: Num
  tier: string
}

export type SignalsResp = { paramsVersion: string; events: SignalEvent[]; phase: string; phaseDate: string }

export type SignalParams = Record<string, number>
export type FullParams = Record<string, unknown> & { signal: SignalParams; weights: number[] }

export type ParamSet = { version: string; name: string; active: boolean; createdAt: string; params: FullParams }
export type ParamsResp = { sets: ParamSet[]; defaults: FullParams; defaultsVersion: string }

export type HStat = { n: number; median: Num; mean: Num; winRate: Num }
export type BtGroup = { source: string; side: number; period: 'all' | 'in' | 'out'; n: number; horizons: HStat[]; maeMedian: Num }
export type BtSignal = SignalEvent & { source: string; side: number; rets: Num[]; mae: Num }
export type BtStrategy = { name: string; label: string; totalRet: number; cagr: number; maxDD: number; exposure: number; switches: number; equity: number[] }
export type BtConfig = { from: string; to: string; split: string; horizons: number[]; maeWindow: number }
export type BtResult = {
  config: BtConfig
  from: string
  to: string
  days: number
  signals: BtSignal[] | null
  groups: BtGroup[] | null
  baseRate: HStat[]
  strategies: BtStrategy[]
  dates: string[]
  close: number[]
  corrNames: string[] | null
  corr: Num[][] | null
  events: SignalEvent[] | null
}
export type BacktestResp = { paramsVersion: string; activeVersion: string; params: FullParams; elapsedMs: number; result: BtResult }

export type Overview = {
  market: string
  index: string
  paramsVersion: string
  params: Params
  last?: DayScore
  prev?: DayScore
  recent?: { date: string; close: Num; tS: Num; v: Num }[]
  phase?: string
  recentEvents?: SignalEvent[] | null
}

export type ScoreSeries = {
  paramsVersion: string
  dates: string[]
  tiers: string[]
  series: Record<'close' | 'v' | 'tS' | 't1' | 't2' | 't3' | 't4', Num[]>
  thresholds: { cheap: number; expensive: number; extreme: number }
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(f: () => void) {
  onUnauthorized = f
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    if (res.status === 401 && path !== '/api/auth/login') onUnauthorized()
    throw new ApiError(res.status, data?.error ?? `HTTP ${res.status}`)
  }
  return data as T
}

export const api = {
  me: () => request<{ ok: boolean }>('GET', '/api/auth/me'),
  login: (password: string) => request<{ ok: boolean }>('POST', '/api/auth/login', { password }),
  logout: () => request<{ ok: boolean }>('POST', '/api/auth/logout'),
  metrics: () => request<Metric[]>('GET', '/api/metrics'),
  series: (metric: string, source = '') =>
    request<Series>('GET', `/api/series?metric=${encodeURIComponent(metric)}&source=${encodeURIComponent(source)}`),
  startSync: (full: boolean) => request<JobStatus>('POST', '/api/jobs/sync', { full }),
  stockStats: () =>
    request<{ stats: { stocks: number; withBars: number; delisted: number; first: string; last: string }; source: string }>('GET', '/api/stocks/stats'),
  startBreadth: (full: boolean) => request<JobStatus>('POST', '/api/jobs/breadth', { full }),
  currentJob: () => request<JobStatus | null>('GET', '/api/jobs/current'),
  overview: () => request<Overview>('GET', '/api/overview'),
  scores: (from = '', to = '') =>
    request<ScoreSeries>('GET', `/api/scores?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`),
  signals: () => request<SignalsResp>('GET', '/api/signals'),
  params: () => request<ParamsResp>('GET', '/api/params'),
  saveParams: (name: string, params: FullParams, activate: boolean) =>
    request<{ version: string }>('POST', '/api/params', { name, params, activate }),
  backtest: (params: FullParams | null, config: { from: string; split: string }) =>
    request<BacktestResp>('POST', '/api/backtest', { params: params ?? undefined, config }),
  jobs: () => request<JobRun[]>('GET', '/api/jobs'),
}
