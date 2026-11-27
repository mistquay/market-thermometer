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
  kind: 'sync' | 'backfill' | 'breadth' | 'breadth-backfill' | 'funds' | 'crosscheck' | 'gapfill' | 'names' | 'ext-backfill' | 'derived'
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
export type BtStrategy = { name: string; label: string; totalRet: number; cagr: number; maxDD: number; exposure: number; switches: number; equity: number[]; position?: number[] }
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
export type NotifyState = {
  kinds: NotifyKind[]; channels: NotifyChannel[]; logs: NotifyLog[]; signals: boolean; since: string; weekly: boolean
  opsAlerts: boolean; scheduleStart: string; scheduleDeadline: string
}
export type AuditEntry = { id: number; at: string; actor: string; action: string; target: string; detail: Record<string, unknown> }
export type PMTickHealth = {
  symbol: string; samples: number; first: number; last: number
  p50Ms: number; p95Ms: number; p99Ms: number; maxGapMs: number; gapsOver10s: number; uptimeRatio: number
}
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
  pmTick?: PMTickHealth[] | null
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
    rebalance?: string; rebalances?: number
    dca?: { monthly: number; months: number; invested: number; finalValue: number; totalRet: number; benchValue: number; benchRet: number } | null
  }
}

export type ScreenerRow = {
  code: string; kind: 'stock' | 'fund'; name: string; date: string; close: number
  ret20: number | null; ret60: number | null; aboveMa200: boolean | null; distMa200: number | null
  peTtm: number | null; pb: number | null; totalMv: number | null; amount: number | null
}
export type ScreenerResp = { asOf: string; universe: number; total: number; elapsedMs: number; items: ScreenerRow[] }
export type ResearchNote = { id: number; target: string; title: string; content: string; tags: string; createdAt: string; updatedAt: string }

export type SecuritySeries = {
  kind: string; code: string; name: string; dates: string[]; close: number[]; adjusted: number[]
  // 扩展字段（只有股票、且回填过时才有），与 dates 对齐
  open?: (number | null)[]; high?: (number | null)[]; low?: (number | null)[]; volume?: (number | null)[]
  peTtm?: (number | null)[]; pb?: (number | null)[]; extFrom?: string
}

export type SimParams = { commissionRate: number; minCommission: number; stampDuty: number; slippageBps: number; lotSize: number; expireDays: number; maxOrderAmount: number }
export type SimAccount = { id: number; name: string; cash: number; start: string; params: SimParams; note: string; createdAt: string }
export type SimOrder = {
  id: number; code: string; kind: string; side: 'buy' | 'sell'; qty: number; placedDate: string; source: string; note?: string
  status: 'pending' | 'filled' | 'rejected' | 'expired' | 'canceled'; fillDate?: string; fillPrice?: number; amount?: number; fee?: number; reason?: string
}
export type SimPosition = { code: string; kind: string; qty: number; cost: number; price: number; priceDate: string; value: number; pnl: number }
export type SimView = {
  account: SimAccount
  state: { cash: number; positions: SimPosition[] | null; equity: number; realized: number; fees: number; curve: { date: string; equity: number; cash: number }[] | null; asOf: string }
  orders: SimOrder[]
  bench: number[] | null
  names: Record<string, string>
  cutoff: string
}
export type SimOrderInput = { code: string; side: 'buy' | 'sell'; qty: number; note?: string }
export type SimPlan = {
  plan: { targets: { Code: string; Kind: string; Weight: number }[]; orders: SimOrderInput[]; note: string }
  strategy: { date: string; tier: string; base: number; overlay: number; weight: number; lastSignal?: string } | null
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

// ---- 加密货币与预测市场 ----

export type Kline = { t: number; o: number; h: number; l: number; c: number; v: number; q: number } // t 为 UTC 毫秒
export type SyncStatus = { running: boolean; lastRun?: string; errors: number; log: string[]; every: string }
export type KlineCoverage = { symbol: string; interval: string; first: number; last: number; count: number; source: string }
export type CryptoState =
  | { configured: false }
  | { configured: true; symbols: string[]; intervals: string[]; status: SyncStatus; coverage: KlineCoverage[] }

export type PMMarket = {
  conditionId: string; slug: string; question: string; groupTitle?: string
  outcomes: string[]; tokens: string[]; prices: number[]
  bestBid: number; bestAsk: number; lastPrice: number; volume: number; volume24h: number; liquidity: number
  start: string; end: string; closed: boolean; accepting: boolean; tickSize: number; minSize: number
  feeType?: string; winner: number; resolution?: string
  feeRate?: number; feeExp?: number // 吃单手续费 = 股数 × feeRate × (p(1−p))^feeExp
}
export type PMEvent = {
  id: string; slug: string; title: string; series?: string; tags: string[]; start: string; end: string
  closed: boolean; negRisk: boolean; volume: number; volume24h: number; liquidity: number; markets: PMMarket[]
  priceToBeat?: number // 短期涨跌盘起始价（结束后才公布；-1 为没有）
}
export type PMSeriesStats = { series: string; events: number; closed: number; first: number; last: number; withHistory: number; resolved: number; firstWins: number }
export type PMState =
  | { configured: false }
  | { configured: true; source: string; series: string[]; status: SyncStatus; stats: PMSeriesStats[]; watched: PMEvent[]; book: PMBookState; shadow?: PMShadowStatus }
export type PMBookTarget = { series: string; slug: string; token: string; symbol?: string; start: string; end: string; conditionId: string; feeRate?: number }
export type PMBookState =
  | { configured: false }
  | { configured: true
      status: { running: boolean; every: string; targets: PMBookTarget[]; ticks: number; books: number; spots: number; lastRun?: string; errors: number; lastErr?: string; errAt?: string; wsEnabled?: boolean; wsSpotOk?: boolean; wsBookOk?: boolean }
      stats: { books: number; tokens: number; spots: number; first: number; last: number } }
export type BookSnap = { t: number; bids: BookLevel[]; asks: BookLevel[] }
export type PMEval = {
  target: PMBookTarget; ok: boolean; reason?: string; p: number; bidUp: number; askUp: number; askDn: number
  edgeUp: number; edgeDn: number; k: number; kSrc: string; spot: number; sdBp: number; tau: number; at: string
}
export type PMShadowStatus = {
  strategy: string; minEdge: number; evals: PMEval[] | null; signals: number; basis: Record<string, number>; basisN: Record<string, number>
  sigma: Record<string, number>; basisErr: Record<string, number>; ptbLag: number; lastTick?: string; lastErr?: string; errAt?: string
}
export type PMSignal = {
  id: number; t: number; strategy: string; slug: string; conditionId: string; outcome: number; p: number; ask: number; askSize: number
  feeRate: number; edge: number; k: number; kSrc: string; sdBp: number; tau: number; spot: number; basis: number; winner: number
}
export type PMBucketStat = {
  key: string; n: number; resolved: number; wins: number; winRate: number
  avgModelP: number; avgAsk: number; avgEdge: number; brier: number; theoreticalPnL: number
}
export type PMCalibBin = { label: string; n: number; meanP: number; actualRate: number; gap: number }
export type PMReplayTrade = {
  id: number; t: number; slug: string; series: string; outcome: number; kSrc: string; tau: number
  p: number; signalAsk: number; fillAvg: number; slippage: number; shares: number; gross: number; fee: number
  pnl: number; won: boolean; status: string; skipReason: string; bookSource: string
}
export type PMEvalConfig = {
  days?: number; minEdge: number; kSrc: string; series: string; firstPerMarket: boolean
  delayMs: number; heartbeatMs: number; clusterMin: number; initialCash: number
  sizing: 'fixed' | 'kelly'; orderUsd: number; kellyFrac: number; maxAssetUsd: number; useFairLimit: boolean
}
export type PMEvalReport = {
  config: PMEvalConfig; rawSignals: number; filteredSignals: number; uniqueMarkets: number; clusters: number
  overall: PMBucketStat; byKSrc: PMBucketStat[]; bySeries: PMBucketStat[]; byTau: PMBucketStat[]
  calibration: PMCalibBin[]
  replay: {
    executed: number; wins: number; losses: number; winRate: number
    skippedStale: number; skippedNoBook: number; skippedRisk: number; skippedNoFill: number
    initialCash: number; finalEquity: number; netPnl: number; totalGross: number; totalFees: number
    roiOnVolume: number; maxDD: number; avgSlippage: number; clusters: number; clusterWinRate: number; clusterSharpe: number
    curve: { t: number; equity: number; cumPnl: number; trades: number }[]
    trades: PMReplayTrade[]
  }
}

export type PricePoint = { t: number; p: number }
export type BookLevel = { price: number; size: number }
export type Book = { token: string; time: string; bids: BookLevel[]; asks: BookLevel[] }

// ---- 预测市场模拟盘 ----
export type PMSimParams = { maxOrderUsd: number; maxDailyUsd: number; maxMarketUsd: number; maxAssetUsd?: number; halted: boolean }
export type PMArbLeg = { conditionId: string; question: string; outcome: string; bestAsk: number; avgAsk: number; askSize: number; feeRate: number }
export type PMArbOpp = {
  kind: 'binary' | 'negrisk'; slug: string; title: string; series?: string; shares: number
  rawSumAsk: number; totalCost: number; payout: number; netProfit: number; netPerUnit: number; roi: number; legs: PMArbLeg[]
}
export type PMArbResp = { scannedEvents: number; scannedTokens: number; targetShares: number; at: string; opportunities: PMArbOpp[] }
export type PMExecConfig = {
  mode: 'shadow' | 'hitl' | 'auto_sim'; accountId: number; sizing: 'fixed' | 'kelly'; orderUsd: number
  kellyFrac: number; kSrc: string; minEdge: number; ttlSec: number; notify: boolean
}
export type PMProposal = {
  id: number; createdAt: number; expiresAt: number; strategy: string; slug: string; conditionId: string
  outcome: number; p: number; ask: number; edge: number; suggestedUsd: number; kSrc: string; tau: number
  status: 'pending' | 'approved' | 'rejected' | 'expired'; accountId: number; orderId?: number; reason?: string
}
export type PMFill = { shares: number; gross: number; fee: number; levels: BookLevel[] | null }
export type PMOrder = {
  id: number; account: number; time: string; slug: string; conditionId: string; token: string; outcome: number; outcomeName: string
  question: string; side: 'buy' | 'sell'; reqShares?: number; reqAmount?: number; limit?: number; source: string
  status: 'filled' | 'partial' | 'rejected'; reason?: string; fill: PMFill; bookTime?: string
}
export type PMPosition = {
  conditionId: string; slug: string; question: string; token: string; outcome: number; outcomeName: string
  shares: number; cost: number; end: string; settled: boolean; payout?: number; mark?: number
}
export type PMSimAccount = { id: number; name: string; cash: number; params: PMSimParams; note: string; createdAt: string }
export type PMAccountView = {
  account: PMSimAccount
  state: { cash: number; positions: PMPosition[] | null; realized: number; fees: number; volume: number; orders: number }
  equity: number; unrealized: number; orders: PMOrder[] | null; globalHalt: boolean
}
export type PMOrderReq = {
  account: number; slug: string; conditionId?: string; outcome: number; side: 'buy' | 'sell'
  shares?: number; amount?: number; limit?: number; dryRun?: boolean
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
  startExtBackfill: () => request<JobStatus>('POST', '/api/jobs/ext-backfill'),
  startGapFill: (source: string, codes: string[]) => request<JobStatus>('POST', '/api/jobs/gapfill', { source, codes }),
  searchSecurities: (q: string) => request<{ items: SecurityHit[]; names: number }>('GET', `/api/securities/search?q=${encodeURIComponent(q)}`),
  securitySeries: (kind: string, code: string) => request<SecuritySeries>('GET', `/api/securities/${kind}/${code}/series`),
  startNames: () => request<JobStatus>('POST', '/api/jobs/names'),
  portfolios: () => request<PortfolioData[]>('GET', '/api/portfolios'),
  savePortfolio: (p: PortfolioData) => (p.id ? request<PortfolioData>('PUT', `/api/portfolios/${p.id}`, p) : request<PortfolioData>('POST', '/api/portfolios', p)),
  deletePortfolio: (id: number) => request<{ ok: boolean }>('DELETE', `/api/portfolios/${id}`),
  analyzePortfolio: (id: number, rebalance = 'none', dca = 0) =>
    request<PortfolioAnalysis>('GET', `/api/portfolios/${id}/analysis?rebalance=${encodeURIComponent(rebalance)}&dca=${dca}`),
  screener: (params: Record<string, string>) =>
    request<ScreenerResp>('GET', `/api/screener?${new URLSearchParams(params).toString()}`),
  notes: (target = '', limit = 50) =>
    request<ResearchNote[]>('GET', `/api/notes?target=${encodeURIComponent(target)}&limit=${limit}`),
  saveNote: (n: { id?: number; target: string; title: string; content: string; tags: string }) =>
    n.id ? request<ResearchNote>('PUT', `/api/notes/${n.id}`, n) : request<ResearchNote>('POST', '/api/notes', n),
  deleteNote: (id: number) => request<{ ok: boolean }>('DELETE', `/api/notes/${id}`),
  simAccounts: () => request<{ accounts: SimAccount[]; defaults: SimParams }>('GET', '/api/sim/accounts'),
  createSimAccount: (a: { name: string; cash: number; start: string; params: SimParams }) => request<{ id: number }>('POST', '/api/sim/accounts', a),
  simAccount: (id: number) => request<SimView>('GET', `/api/sim/accounts/${id}`),
  deleteSimAccount: (id: number) => request<{ ok: boolean }>('DELETE', `/api/sim/accounts/${id}`),
  placeSimOrders: (id: number, source: string, orders: SimOrderInput[]) => request<{ ids: number[] }>('POST', `/api/sim/accounts/${id}/orders`, { source, orders }),
  cancelSimOrder: (id: number, oid: number) => request<{ ok: boolean }>('POST', `/api/sim/accounts/${id}/orders/${oid}/cancel`),
  planSimRebalance: (id: number, body: { portfolioId?: number; strategyCode?: string }) => request<SimPlan>('POST', `/api/sim/accounts/${id}/rebalance`, body),
  plugins: () => request<PluginInfo[]>('GET', '/api/plugins'),
  savePlugin: (kind: string, name: string, body: { enabled: boolean; config: Record<string, string> }) =>
    request<{ ok: boolean }>('PUT', `/api/plugins/${kind}/${name}`, body),
  testPlugin: (kind: string, name: string) =>
    request<{ target: string; count: number; elapsedMs: number; last?: unknown; error?: string }>('POST', `/api/plugins/${kind}/${name}/test`),
  notify: () => request<NotifyState>('GET', '/api/notify'),
  saveNotifySettings: (body: { signals?: boolean; weekly?: boolean; opsAlerts?: boolean; scheduleStart?: string; scheduleDeadline?: string }) =>
    request<NotifyState>('POST', '/api/notify/settings', body),
  audits: (action = '', limit = 100) =>
    request<AuditEntry[]>('GET', `/api/audit?action=${encodeURIComponent(action)}&limit=${limit}`),
  saveChannel: (id: number, c: ChannelInput) =>
    id ? request<NotifyChannel>('PUT', `/api/notify/channels/${id}`, c) : request<NotifyChannel>('POST', '/api/notify/channels', c),
  deleteChannel: (id: number) => request<{ ok: boolean }>('DELETE', `/api/notify/channels/${id}`),
  testChannel: (id: number) => request<{ id: number; name: string; error?: string }>('POST', `/api/notify/channels/${id}/test`),
  crypto: () => request<CryptoState>('GET', '/api/crypto'),
  cryptoKlines: (symbol: string, interval: string, to = 0, limit = 1500) =>
    request<{ klines: Kline[]; more: boolean }>('GET', `/api/crypto/klines?symbol=${encodeURIComponent(symbol)}&interval=${encodeURIComponent(interval)}&to=${to}&limit=${limit}`),
  cryptoPrice: (symbol: string) => request<{ symbol: string; price: number; source: string; time: string }>('GET', `/api/crypto/price?symbol=${encodeURIComponent(symbol)}`),
  syncCrypto: () => request<{ ok: boolean }>('POST', '/api/crypto/sync'),
  pm: () => request<PMState>('GET', '/api/pm'),
  pmEvents: (series: string, state: 'upcoming' | 'closed', limit = 50, before = 0) =>
    request<PMEvent[]>('GET', `/api/pm/events?series=${encodeURIComponent(series)}&state=${state}&limit=${limit}&before=${before}`),
  pmEvent: (slug: string) => request<{ event: PMEvent; watched: boolean }>('GET', `/api/pm/events/${encodeURIComponent(slug)}`),
  pmSearch: (q: string) => request<PMEvent[]>('GET', `/api/pm/search?q=${encodeURIComponent(q)}`),
  pmPrices: (token: string, from = 0, to = 0, fidelity = 0) =>
    request<{ points: PricePoint[]; source: string }>('GET', `/api/pm/prices?token=${encodeURIComponent(token)}&from=${from}&to=${to}&fidelity=${fidelity}`),
  pmSignals: (since = 0) => request<PMSignal[]>('GET', `/api/pm/signals?since=${since}`),
  pmEval: (cfg: Partial<PMEvalConfig>) => request<{ report: PMEvalReport; elapsedMs: number }>('POST', '/api/pm/eval', cfg),
  pmArb: (shares = 50) => request<PMArbResp>('GET', `/api/pm/arb?shares=${shares}`),
  pmExec: () => request<PMExecConfig>('GET', '/api/pm/exec'),
  pmExecSave: (cfg: PMExecConfig) => request<PMExecConfig>('POST', '/api/pm/exec', cfg),
  pmProposals: () => request<PMProposal[]>('GET', '/api/pm/proposals'),
  pmProposalApprove: (id: number, body: { accountId?: number; amount?: number }) =>
    request<{ order: PMOrder }>('POST', `/api/pm/proposals/${id}/approve`, body),
  pmProposalReject: (id: number) => request<{ ok: boolean }>('POST', `/api/pm/proposals/${id}/reject`),
  pmBooks: (token: string, from = 0, to = 0) => request<BookSnap[]>('GET', `/api/pm/books?token=${encodeURIComponent(token)}&from=${from}&to=${to}`),
  pmBook: (token: string) => request<Book>('GET', `/api/pm/book?token=${encodeURIComponent(token)}`),
  pmWatch: (slug: string) => request<{ ok: boolean }>('POST', '/api/pm/watch', { slug }),
  pmUnwatch: (slug: string) => request<{ ok: boolean }>('DELETE', `/api/pm/watch/${encodeURIComponent(slug)}`),
  syncPM: () => request<{ ok: boolean }>('POST', '/api/pm/sync'),
  pmSim: () => request<{ accounts: PMAccountView[]; globalHalt: boolean; defaults: PMSimParams }>('GET', '/api/pm/sim'),
  pmSimCreate: (name: string, cash: number, params?: PMSimParams) => request<{ id: number }>('POST', '/api/pm/sim/accounts', { name, cash, params }),
  pmSimAccount: (id: number) => request<PMAccountView>('GET', `/api/pm/sim/accounts/${id}`),
  pmSimParams: (id: number, p: PMSimParams) => request<{ ok: boolean }>('PUT', `/api/pm/sim/accounts/${id}/params`, p),
  pmSimDelete: (id: number) => request<{ ok: boolean }>('DELETE', `/api/pm/sim/accounts/${id}`),
  pmSimOrder: (o: PMOrderReq) => request<PMOrder>('POST', '/api/pm/sim/orders', o),
  pmHalt: (on: boolean) => request<{ on: boolean }>('POST', '/api/pm/halt', { on }),
}
