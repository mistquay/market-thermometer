# 部署说明（大陆云服务器）

> 2026-11-25 写。部署产物是**一个二进制 + 一个 SQLite 文件 + config.yaml**。纯 Go（`modernc.org/sqlite`），不需要 CGO，交叉编译即可。

## 1. 构建

在开发机上（Windows 也可以交叉编译 Linux 版本）：

```bash
cd web && pnpm install && pnpm build && cd ..      # 前端产物输出到 internal/webui/dist，嵌入二进制
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o mt ./cmd/mt
```

PowerShell 下设置环境变量：`$env:GOOS='linux'; $env:GOARCH='amd64'; go build ...`，构建完记得 `Remove-Item Env:GOOS, Env:GOARCH`。

## 2. 服务器目录与配置

```
/opt/mt/
├── mt              # 二进制
├── config.yaml     # 权限 600，含口令哈希
└── data/mt.db      # SQLite（WAL 模式，旁边会有 mt.db-wal / mt.db-shm）
```

```bash
sudo useradd --system --home /opt/mt --shell /usr/sbin/nologin mt
sudo mkdir -p /opt/mt/data && sudo chown -R mt:mt /opt/mt
sudo -u mt /opt/mt/mt hashpw        # 输入口令，把输出的 admin_password_hash 填入 config.yaml
sudo chmod 600 /opt/mt/config.yaml
```

`config.yaml` 要点（完整字段见 `config.example.yaml`）：

- `listen: 127.0.0.1:8080`：只监听本机，由 Caddy 对外提供 HTTPS。
- `db_path: data/mt.db`：相对路径以工作目录为准（systemd 里设为 `/opt/mt`）。
- `http_proxy`：只给 Telegram 用，大陆服务器直连不了 `api.telegram.org`。可以填海外 HTTP 代理，或者不用 Telegram 渠道。A 股数据源一律直连。
- `stocks`：服务器上设为 `sources: []`、`skip_funds: true`（见第 6 节）。

## 3. systemd

`/etc/systemd/system/mt.service`：

```ini
[Unit]
Description=market thermometer
After=network-online.target
Wants=network-online.target

[Service]
User=mt
Group=mt
WorkingDirectory=/opt/mt
ExecStart=/opt/mt/mt serve -c /opt/mt/config.yaml -trust-proxy
Restart=on-failure
RestartSec=5
# 加固
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/mt/data
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now mt
journalctl -u mt -f        # 调度、推送的日志都在这里（“调度：…”开头）
```

- `-trust-proxy`：登录限速按客户端 IP 计数，必须在反代之后才开，并且反代要设置 `X-Real-IP`（见下节）。直接暴露端口时不要开，否则客户端可以伪造 IP 绕过限速。
- `-schedule=false`：关闭内置的每日任务（交易日 17:30 同步，不完整时每 30 分钟重试，22:00 截止，完成后推送）。只有在别处负责同步时才关。
- 首次部署可以先在本机跑完回填，再把 `data/mt.db` 拷上去（见第 5、6 节），不用在服务器上从零回填。

## 4. Caddy（自动 HTTPS）

`/etc/caddy/Caddyfile`：

```caddy
mt.example.com {
	encode gzip
	reverse_proxy 127.0.0.1:8080 {
		header_up X-Real-IP {remote_host}
	}
}
```

- 大陆服务器的域名需要 ICP 备案，80 / 443 端口才能正常访问。
- 安全组只开放 22、80、443，不要开放 8080。
- Caddy 默认透传 `Host`，程序的同源校验（写接口检查 `Origin` 与 `Host` 一致）不需要额外配置。

## 5. 数据库备份

SQLite 处于 WAL 模式，**不要直接 `cp` 正在使用的 mt.db**（可能拷到不一致的状态）。用 SQLite 自带的在线备份：

```bash
sudo apt install sqlite3
sudo -u mt sqlite3 /opt/mt/data/mt.db ".backup '/opt/mt/backup/mt-$(date +%F).db'"
```

每天 23:30 备份（在 22:00 调度截止之后），保留 14 天。`/etc/cron.d/mt-backup`：

```cron
30 23 * * * mt mkdir -p /opt/mt/backup && sqlite3 /opt/mt/data/mt.db ".backup '/opt/mt/backup/mt-$(date +\%F).db'" && find /opt/mt/backup -name 'mt-*.db' -mtime +14 -delete
```

- 数据库约 800MB，主要是个股日线。异地保存时压缩一下（`zstd -T0`），能小很多。
- 恢复：`systemctl stop mt`，用备份文件替换 `data/mt.db`，删除旧的 `mt.db-wal`、`mt.db-shm`，再 `systemctl start mt`。
- 推送渠道的 webhook、secret 存在库里（`notify_channel` 表），备份文件要按敏感数据保管。

## 6. 服务器上的个股数据源（2026-11-25 已定：方案 A）

stockdb 只在你本机运行，服务器上没有。腾讯能当个股源，但实测会被 WAF 拦截：

- 2026-11-25 第一次：每 0.4 秒一次请求，约 380 次后被拦截。
- 30 分钟后第二次：每 1.5 秒一次请求，约 160 次后又被拦截。

每日增量同步全部约 5,500 只股票，每只至少一次请求，**目前不可行**。

| 方案 | 做法 | 代价 |
|---|---|---|
| A. 服务器不同步个股 | 服务器 `stocks.sources: []`，只同步指标（东财、中证）。市场广度用库里已有的个股日线，不再更新 | 广度停在拷贝时的日期。T3 目前权重为 0，不影响打分；数据质量页会显示“个股日线落后” |
| B. 本机同步后上传 | 本机每天跑完同步（stockdb 可用），再把库上传到服务器（`sqlite3 .backup` 后 `rsync`，或只导出增量） | 本机要每天开机；整库约 800MB，增量导出需要另写工具 |
| C. 腾讯限量增量 | 服务器用腾讯，但每天只同步一部分股票（轮转），几天覆盖一轮 | 广度有几天滞后；WAF 规则不明，仍可能被封 |

**已定方案 A**：服务器配置 `stocks.sources: []`、`stocks.skip_funds: true`，只同步指标。温度计打分不依赖个股数据；以后要启用广度（T3）时再考虑 B。场内基金日线由用户另写脚本获取后导入。

## 7. 升级

```bash
scp mt server:/tmp/mt.new
ssh server 'sudo install -o mt -g mt -m 755 /tmp/mt.new /opt/mt/mt && sudo systemctl restart mt'
```

启动时会自动迁移表结构，并按生效参数重算得分。升级前建议先备份一次数据库（第 5 节）。
