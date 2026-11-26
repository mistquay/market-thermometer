# 加密货币与 Polymarket（2026-11-26）

用户方向（2026-11-26）：接入加密货币市场，**重点是 Polymarket**，自动交易围绕 Polymarket 来做。

- **策略**：
  - 加密短期涨跌盘：BTC / ETH 的 5 分钟、15 分钟、1 小时 Up or Down；
  - 套利 / 错价；
  - 跟单 / 事件信号。
  - 不做做市。
- **下单**：在用户的海外服务器上。
- **授权逐级放开**：模拟盘 → 每笔人工确认 → 单笔 / 单日限额内自动 → 全自动，任何阶段都有一键熔断。
- **私钥**：钱包私钥只存在本机加密文件或环境变量里，不进数据库，不经过网页。

## 1. 现状

| 部分 | 位置 | 说明 |
|---|---|---|
| 加密 K 线插件 | `internal/datasource/{binance,okx}` | 币安 `/api/v3/klines`，OKX 备用。只取已收盘的 K 线，时间为 UTC 毫秒 |
| 加密 K 线存储与同步 | `store/crypto.go`、`job/crypto.go` | 表 `crypto_bar`。7×24 每 15 分钟同步一次，首次按 `crypto.history` 回填，之后从最后一根往回重拉 2 根 |
| 预测市场插件接口 | `internal/datasource/prediction.go` | 事件 → 市场 → 结果代币。只读：`Events` / `EventsAfter`（游标）/ `Search` / `PriceHistory` / `Book` |
| Polymarket 插件 | `internal/datasource/polymarket` | Gamma（元数据、搜索）+ CLOB（订单簿、价格历史）公共接口，不需要钱包 |
| 存储 | `store/polymarket.go` | `pm_event`、`pm_market`（完整字段存 JSON）、`pm_price`、`pm_watch`、`pm_series_sync`（系列水位） |
| 同步 | `job/polymarket.go` | 7×24 每 10 分钟同步一次，详见 §3 |
| Web | 加密页、预测页 | 接口 `/api/crypto*`、`/api/pm*` |
| CLI | `mt crypto`、`mt polymarket` | 各手动同步一轮，用于调试和运维 |

## 2. 接口实测要点

- **Gamma `/events`**：
  - `limit` 最大 100；超过 50 条只返回 gzip，Go 会自动解压。
  - **offset 超过约 2,000 返回 422**，深翻页要用 `/events/keyset`。它返回 `{events, next_cursor}`，下一页用 **`after_cursor`** 参数，`cursor` / `next_cursor` 都会被忽略。
  - `end_date_min` 可以过滤掉早该结束却仍标着 active 的陈旧事件，5m 系列有一批 2025-12 的这种事件。
  - 可以重复传 `slug=` 一次取多个事件。
- **字段**：`outcomes`、`clobTokenIds`、`outcomePrices` 是 JSON 字符串形式的数组；数字有时是字符串。
- **结算判定**：短期涨跌盘不走 UMA，`umaResolutionStatus` 为空。插件的判定条件是：已关闭、UMA 状态为空或 `resolved`、价格恰好为 1 / 0。UMA 状态为 proposed、disputed 等时视为未结算。
- **涨跌盘系列**：
  - 系列名为 `{btc,eth,sol,xrp}-up-or-down-{5m,15m,hourly,4h,daily}`，其中 sol 没有 hourly 和 daily。
  - 事件 slug 形如 `btc-updown-15m-<开始 Unix 秒>`，市场的 `eventStartTime` 是计价区间的开始时间。
  - **按 Chainlink BTC/USD 结算，不是币安价**，回测和定价时要考虑两者的差异。
  - 下单参数：tick 0.01，最小下单 5，手续费类型 `crypto_btc_5m_15m_fees`。**费率公式尚未核实**，模拟盘之前要查清楚，不能按 `takerBaseFee` 猜。
- **成交额**（2026-11-26 的 24 小时）：btc 5m 约 1,220 万美元，btc 15m 约 230 万，eth 5m 约 110 万，btc hourly 约 48 万。
- **CLOB `/prices-history`**：fidelity 以分钟为单位，每次请求约 0.5 秒，同步时 4 路并发，约 7 个市场 / 秒。

## 3. 同步策略（`job.PMSyncer`）

每个系列按以下顺序处理：

1. **已结算事件**：用游标按结束时间倒序翻页，直到上次的水位 `through`。
   - 水位表 `pm_series_sync(since, through)` **只在一轮分页完整成功后才更新**，中途失败不会留下空洞，下一轮会重翻。
   - 本地连续覆盖到回溯起点（`history_days`，留 1 天余量）之前，每轮都翻到回溯起点。
2. **已关闭但还没出结果的事件**：按 slug 批量刷新，每 20 个一次请求，每轮最多 60 个。
3. **进行中 / 即将开始的事件**：用 `end_date_min=now` 过滤后取前 100 个。
4. **价格历史**：对本地缺价格历史的已结算市场，拉取第一个结果代币（Up / Yes）的 1 分钟价格。
   - 时间范围是计价开始前同样长的一段到结束，因为短期盘在计价区间开始前就可以交易。
   - 每轮预算 2,400 个市场，按系列平分，前面没用完的留给后面。
   - 单个市场失败只计数，下一轮重试；累计 10 个失败才中止该系列。
   - 没有成交的市场存一个占位点：结束时间加结算价 0 / 1。**回测读取时要识别并排除**：只有一个点，且 t 等于结束时间。

关注的事件每轮刷新元数据，并按小时增量拉取各市场第一个代币的价格。

## 4. 数据的局限（做策略前必须知道）

- 价格历史是**成交 / 中间价的 1 分钟采样**：5m 盘每个市场只有约 10 个点，没有盘口和深度。用它回测短期盘和套利会**明显偏乐观**。
- **历史盘口无法回填**，只能从现在开始采集。下一步应先采集进行中市场的 `/book` 快照（或 CLOB WebSocket）和同一时刻的币安价，再做模拟盘的成交模型。
- 近 14 天本地统计的 Up 胜率：btc 15m 46.1%，btc hourly 42.9%，5m 约 48%，同期 BTC 整体下跌。这只是样本描述，不是策略依据。

## 5. 下单与地区限制

- 下单前必须检查 `https://polymarket.com/api/geoblock`，被限制地区的下单会被拒绝。本机出口 IP（日本）返回 `blocked: true`。按文档，日本只限制前端，API 不受限，但实盘一律以海外服务器为准。
- **认证**：L1 用 EIP-712 钱包签名创建或派生 API Key；L2 用 HMAC-SHA256 的 `POLY_*` 请求头。链为 Polygon（137）。
- **客户端库**：官方 clob-client v1 已归档，v2 有 TS / Python / Rust 版本。Go 的 `go-order-utils` 最后更新于 2025-09，可能不兼容 v2。实盘下单计划写成**外部进程插件**（Python，py-clob-client v2，协议见 `docs/plugin-protocol.md`），只部署在海外服务器。

## 6. 下一步

1. **盘口采集**：进行中市场的订单簿快照，加同时刻的币安价，存本地。
2. **模拟盘（二元结果）**：
   - 吃单按盘口逐档成交，加上手续费；
   - 到结算时按 0 / 1 兑付；
   - 风控：单笔 / 单日限额、一键熔断。
3. **策略引擎**：
   - 短期涨跌盘：用币安现价相对计价起点的偏离和波动率估算公允概率，和盘口比较；
   - 套利扫描：二元市场 Yes + No、互斥多结果的 Yes 之和；
   - 跟单 / 事件信号：Data API v2。
4. **实盘插件**（海外服务器）：先做到每笔人工确认，再按用户授权逐级放开。
