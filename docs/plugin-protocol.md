# 外部进程插件协议（v1）

> 2026-11-25。外部插件是一个独立程序（任何语言），通过**标准输入输出上按行分隔的 JSON-RPC 2.0** 与核心通信。
> Python 可直接使用 `examples/plugins/sdk/mtplugin.py`（单文件，只用标准库），示例见 `examples/plugins/csv_source`。

## 1. 安装与安全边界

插件目录里要有清单 `plugin.json`，在 `config.yaml` 中启用：

```yaml
plugins:
  - dir: examples/plugins/csv_source       # 插件目录，也是进程的工作目录
    command: [python, -I, main.py]          # 可执行文件与参数
    timeout_s: 60                           # 单次调用超时，默认 60 秒
    config: {path: sample.csv}              # 下发给插件的配置
```

- **可执行文件与参数只能在 config.yaml 配置**。Web 只能启停插件、修改清单声明的配置字段，相当于不能执行任意命令。
- 插件运行在独立进程中，崩溃不影响核心。核心的保护措施：
  - 每次调用有超时，超时就结束进程，下次调用时重启。
  - 10 分钟内最多启动 5 次，超过后暂停调用。
  - 单条消息不超过 32MB。
  - 返回的数据一律按不可信输入校验：日期格式、有限数值、点数上限、区间裁剪、去重。
- 进程环境会额外设置 `PYTHONIOENCODING=utf-8` 和 `PYTHONUNBUFFERED=1`。所有消息都是 UTF-8。

## 2. 清单 plugin.json

```json
{
  "name": "csv_source",                 // 小写字母、数字、-、_，1–40 个字符
  "kind": "metric_source",              // 见下表
  "version": "0.1.0",
  "label": "CSV 文件指标源",
  "description": "……",
  "metrics": [{"id": "user.csv_demo", "name": "……", "unit": "", "market": "CN", "lagDays": 0}],
  "capabilities": [],
  "config": [{"key": "path", "label": "CSV 文件路径", "help": "……", "type": "", "secret": false, "required": false}]
}
```

- 启动时只读清单，不启动进程；第一次调用时才启动。
- 清单里 `metrics` 声明的新指标会登记到指标目录，可以在 `sources.routes` 中引用。
- 带 `secret: true` 的配置字段，在 Web 和 API 中打码，在错误信息中擦除。

| kind | 说明 | 外部插件支持 |
|---|---|---|
| `metric_source` | 指标数据源 | ✅ |
| `notifier` | 推送渠道 | ✅ |
| `stock_source` / `fund_source` | 个股日线 / 基金净值源 | 预留（方法名见 §4） |
| `derived` / `strategy` | 派生指标 / 策略 | 预留 |
| `broker` | 交易通道 | 只设计接口，不实现（§5） |

## 3. 消息格式

每条消息占一行（JSON 中不能有换行），stdout 只能输出协议消息，日志写 stderr（核心保留最近 50 行，在插件管理页可见）。

```
→ {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol":1,"config":{"path":"sample.csv"}}}
← {"jsonrpc":"2.0","id":1,"result":{}}
→ {"jsonrpc":"2.0","id":2,"method":"metric.fetch","params":{"metric":"user.csv_demo","from":"2026-01-01","to":"2026-11-25"}}
← {"jsonrpc":"2.0","id":2,"result":{"points":[{"date":"2026-11-24","value":1.55}]}}
← {"jsonrpc":"2.0","id":3,"error":{"code":-32000,"message":"上游接口不可用"}}
```

- 核心对每个进程串行发送请求，一次只等一个响应。
- 返回 `error` 表示业务错误，进程会继续使用。超时、进程退出、id 不匹配会导致核心结束进程。
- 进程启动后的第一条请求固定是 `initialize`。配置在 Web 上修改后，核心会重启进程，重新初始化。
- `initialize` 返回错误时（例如配置不对），这次调用失败，错误信息会显示在 Web 上。

## 4. 方法

| 方法 | 参数 | 返回 |
|---|---|---|
| `initialize` | `{protocol, config}` | `{}` |
| `shutdown` | 无 | `{}`，随后进程应退出 |
| `metric.fetch` | `{metric, from, to}`（YYYY-MM-DD） | `{points: [{date, value}]}`；只应返回已收盘定稿的数据 |
| `notify.send` | `{config, title, lines, markdown, text}`（`config` 是该推送渠道的配置） | `{}` |
| `stocks.list`（预留） | `{}` | `{stocks: [{code, kind, delisted}]}` |
| `stocks.bars`（预留） | `{code, from, to}` | `{bars: [{date, close, mult}]}`：不复权收盘价 + 除权乘数 |
| `funds.nav`（预留） | `{code, from}` | `{navs: [{date, nav, accNav}]}` |

## 5. 交易通道（只设计，未实现）

券商与终端尚未确定，P5 之前只定接口。订单、持仓、资金模型由核心维护，与券商无关。插件只负责和券商通信：

| 方法 | 参数 | 返回 |
|---|---|---|
| `broker.account` | `{}` | `{cash, frozen, positions: [{code, qty, available, cost}]}` |
| `broker.place` | `{client_order_id, code, side: buy/sell, qty, price_type: limit/market, price}` | `{broker_order_id}` |
| `broker.cancel` | `{broker_order_id}` | `{}` |
| `broker.orders` | `{since}` | `{orders: [{broker_order_id, client_order_id, status, filled_qty, avg_price}]}` |

约束（见 roadmap §3）：
- 风控检查由核心在调用 `broker.place` 之前强制执行，插件不能绕过。
- `client_order_id` 由核心生成，保证幂等。
- 券商凭证只存在本机，不经过 Web。

## 6. 用 akshare 写指标源（片段）

akshare 是第三方库，需要自行安装（`pip install akshare`），项目本身不依赖它。

```python
import akshare as ak
from mtplugin import Plugin, PluginError

plugin = Plugin()

@plugin.method("metric.fetch")
def fetch(metric, from_, to):
    if metric != "user.cn_m2_yoy":
        raise PluginError("不支持的指标：" + metric)
    df = ak.macro_china_money_supply()          # 字段名以 akshare 当前版本为准
    ...                                           # 转成 [{"date": "YYYY-MM-DD", "value": float}]
    return {"points": points}

plugin.run()
```
