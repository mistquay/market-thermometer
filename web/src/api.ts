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
  kind: 'sync' | 'backfill' | 'breadth' | 'breadth-backfill' | 'funds' | 'crosscheck' | 'gapfill' | 'names'
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

export type FundInfo = { code: string; name: string; type: string }
export type FundNAV = { date: string; nav: number; accNav: number }
export type WatchedFund = FundInfo & { first: string; last: string; count: number; nav: number; accNav: number; addedAt: string }

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
export type RobustOutcome = {
  buySignals: number; sellSignals: number; few: boolean; error?: string
  strategies: { name: string; totalRet: number; cagr: number; maxDD: number }[]
}
export type RobustResp = {
  paramsVersion: string; ratio: number; from: string; elapsedMs: number
  base: RobustOutcome
  rows: { group: string; label: string; level: number; values: string; skip?: string; outcome?: RobustOutcome }[]
  years: { from: string; to: string; label: string; outcome: RobustOutcome }[]
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

export type NotifyField = { key: string; label: string; help?: string; type?: string; secret?: boolean; required?: boolean }
export type NotifyKind = { name: string; label: string; fields: NotifyField[] }
export type NotifyChannel = { id: number; kind: string; kindLabel: string; name: string; config: Record<string, string>; enabled: boolean }
export type NotifyLog = { key: string; title: string; result: string; sentAt: string }
export type NotifyState = { kinds: NotifyKind[]; channels: NotifyChannel[]; logs: NotifyLog[]; signals: boolean; since: string; weekly: boolean }
export type ChannelInput = { kind: string; name: string; config: Record<string, string>; enabled: boolean }

export type QRun = { from: string; to: string; days: number }
export type QDiff = { code: string; src: string; date: string; kind: string; local: number; source: number; recent: boolean }
export type CrossSource = {
  source: string; codes: number; compared: number; close: number; mult: number
  onlySource: number; onlyLocal: number; recent: number; errors?: string[]; skipped?: string
}
export type QualityReport = {
  calendarLast: string
  freshness: { id: string; name: string; source: string; last: string; expected: string; behind: number }[]
  coverage: { date: string; count: number; low: boolean }[] | null
  windowFrom: string
  gapStocks: number
  gapDays: number
  gaps: { code: string; missing: number; runs: QRun[] }[]
  crossCheck: { at: string; from: string; recentFrom: string; codes: string[]; sources: CrossSource[]; diffs: QDiff[] | null; truncated: boolean } | null
  elapsedMs: number
}

export type PluginInfo = {
  id: string; name: string; kind: string; kindLabel: string; version: string; label: string; description?: string
  builtin: boolean; enabled: boolean; state: string; editable: boolean; testable: boolean
  metrics?: { id: string; name?: string; unit?: string }[]
  capabilities?: string[]
  config?: NotifyField[]
  values: Record<string, string>
  process?: { running: boolean; disabled: boolean; pid?: number; starts: number; lastError?: string; stderr: string[]; command: string[]; dir: string }
}

export type SecurityHit = { code: string; kind: 'stock' | 'fund' | 'ofund'; name: string; type?: string; hasData: boolean; delisted?: boolean }
export type PortfolioItem = { kind: string; code: string; weight: number; name?: string }
export type PortfolioData = { id: number; name: string; note: string; start: string; items: PortfolioItem[]; updatedAt: string }
export type PfStats = { totalRet: number; cagr: number; vol: number; maxDD: number; sharpe: number | null }
export type PortfolioAnalysis = {
  portfolio: PortfolioData
  elapsedMs: number
  result: {
    from: string; to: string; dates: string[]; nav: number[]; benchNav: number[]; drawdown: number[]
    stats: PfStats; bench: PfStats; beta: number | null; benchCorr: number | null
    assets: (PfStats & { key: string; label: string; weight: number; endWeight: number; first: string; last: string; stale: boolean; contribution: number })[]
    corrKeys: string[]; corr: (number | null)[][]
    years: { year: string; ret: number; bench: number; excess: number }[]
    missing: string[]; note?: string
  }
}

export type SecuritySeries = { kind: string; code: string; name: string; dates: string[]; close: number[]; adjusted: number[] }

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
    request<{ stats: { stocks: number; withBars: number; delisted: number; funds: number; first: string; last: string }; source: string }>('GET', '/api/stocks/stats'),
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
  robust: (params: FullParams | null, config: { from: string; split: string }) =>
    request<RobustResp>('POST', '/api/backtest/robust', { params: params ?? undefined, config }),
  funds: () => request<{ watched: WatchedFund[]; infoCount: number; configured: boolean }>('GET', '/api/funds'),
  searchFunds: (q: string) => request<FundInfo[]>('GET', `/api/funds/search?q=${encodeURIComponent(q)}`),
  watchFund: (code: string) => request<{ ok: boolean }>('POST', '/api/funds/watch', { code }),
  unwatchFund: (code: string) => request<{ ok: boolean }>('DELETE', `/api/funds/watch/${code}`),
  fundNAV: (code: string) => request<FundNAV[]>('GET', `/api/funds/${code}/nav`),
  startFunds: () => request<JobStatus>('POST', '/api/jobs/funds'),
  jobs: () => request<JobRun[]>('GET', '/api/jobs'),
  quality: () => request<QualityReport>('GET', '/api/quality'),
  startCrossCheck: (codes: string[]) => request<JobStatus>('POST', '/api/jobs/crosscheck', { codes }),
  startGapFill: (source: string, codes: string[]) => request<JobStatus>('POST', '/api/jobs/gapfill', { source, codes }),
  searchSecurities: (q: string) => request<{ items: SecurityHit[]; names: number }>('GET', `/api/securities/search?q=${encodeURIComponent(q)}`),
  securitySeries: (kind: string, code: string) => request<SecuritySeries>('GET', `/api/securities/${kind}/${code}/series`),
  startNames: () => request<JobStatus>('POST', '/api/jobs/names'),
  portfolios: () => request<PortfolioData[]>('GET', '/api/portfolios'),
  savePortfolio: (p: PortfolioData) => (p.id ? request<PortfolioData>('PUT', `/api/portfolios/${p.id}`, p) : request<PortfolioData>('POST', '/api/portfolios', p)),
  deletePortfolio: (id: number) => request<{ ok: boolean }>('DELETE', `/api/portfolios/${id}`),
  analyzePortfolio: (id: number) => request<PortfolioAnalysis>('GET', `/api/portfolios/${id}/analysis`),
  plugins: () => request<PluginInfo[]>('GET', '/api/plugins'),
  savePlugin: (kind: string, name: string, body: { enabled: boolean; config: Record<string, string> }) =>
    request<{ ok: boolean }>('PUT', `/api/plugins/${kind}/${name}`, body),
  testPlugin: (kind: string, name: string) =>
    request<{ target: string; count: number; elapsedMs: number; last?: unknown; error?: string }>('POST', `/api/plugins/${kind}/${name}/test`),
  notify: () => request<NotifyState>('GET', '/api/notify'),
  saveNotifySettings: (body: { signals?: boolean; weekly?: boolean }) => request<NotifyState>('POST', '/api/notify/settings', body),
  saveChannel: (id: number, c: ChannelInput) =>
    id ? request<NotifyChannel>('PUT', `/api/notify/channels/${id}`, c) : request<NotifyChannel>('POST', '/api/notify/channels', c),
  deleteChannel: (id: number) => request<{ ok: boolean }>('DELETE', `/api/notify/channels/${id}`),
  testChannel: (id: number) => request<{ id: number; name: string; error?: string }>('POST', `/api/notify/channels/${id}/test`),
}
