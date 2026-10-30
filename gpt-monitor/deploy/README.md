# 205 服务器部署

项目部署目录：`/home/work/gpt-monitor`。

## 1. 创建只读数据库角色

先在目标服务器或通过 SSH 设置：

```bash
export MONITOR_DB_PASSWORD='<随机强密码>'
./deploy/setup-readonly-role.sh
```

脚本是幂等的，全程只做 `GRANT` 和一个只读视图，**不改 sub2api 的任何表结构或数据**。它授予：

| 对象 | 权限 | 说明 |
| --- | --- | --- |
| `accounts` | 整表 `SELECT` | 续费看板与打票看板共用 |
| `usage_logs` | 列级 `SELECT`（12 列） | 降智率 / RPM / 并发；`user_id`、`ip_address`、`user_agent` 等 PII 列**不授予** |
| `groups`、`account_groups` | 列级 `SELECT` | 分池统计 |
| `gpt_monitor_codex_settings` | 视图 `SELECT` | 只暴露 `openai_codex_ticket%` 的键，代理 URL 脱敏成 `[set]` |

**为什么打票配置必须走视图**：`settings` 表里存着明文 `admin_api_key`（sub2api 的管理员密钥），整表 `SELECT` 会把它暴露给监控进程。视图默认以属主权限执行，所以角色不需要 `settings` 本表的任何权限。

脚本末尾会自动自检：以 `gpt_monitor_ro` 身份尝试读 `settings` 全表和 `usage_logs.ip_address`，两者都必须被拒绝，否则脚本报错退出。

**已知的一处未收窄**：`accounts.credentials` 列含 OAuth refresh token，但邮箱、套餐类型、套餐到期也都在这个 JSON 列里，列级权限无法再切细。要彻底去掉这份暴露，得另建一个只投影所需 JSON 路径的账号视图，并把两个看板的查询都改到视图上——这会改动已在线上跑的续费看板查询，所以没有顺手做。

## 2. 创建 `.env`

```bash
cp .env.example .env
chmod 600 .env
```

至少替换：

- `DATABASE_URL` 中的数据库密码
- `MONITOR_ADMIN_PASSWORD_HASH`
- `SESSION_SECRET`

生成密码哈希：

```bash
printf '%s' '<管理员密码>' | ./gpt-pro-monitor hash-password
```

## 3. 启动容器

容器必须加入生产网络 `sub2api_sub2api-network`：

```bash
docker compose build
docker compose up -d
docker compose ps
curl -fsS http://127.0.0.1:8095/healthz
```

## 4. 配置 Caddy

把 `deploy/Caddyfile.snippet` 合并到现有 `/etc/caddy/Caddyfile`，确认 DNS：

```bash
dig +short gpt-monitor.lmu.ai A
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

Caddy 只反代 `127.0.0.1:8095`，不会再开放额外公网端口。

## 回滚

```bash
docker compose down
# 从 Caddyfile 删除 gpt-monitor.lmu.ai 站点并 reload
docker exec -i sub2api-postgres psql -U sub2api -d sub2api \
  -c 'DROP VIEW IF EXISTS gpt_monitor_codex_settings;' \
  -c 'REVOKE ALL PRIVILEGES ON TABLE accounts, usage_logs, groups, account_groups FROM gpt_monitor_ro;' \
  -c 'DROP ROLE IF EXISTS gpt_monitor_ro;'
```

回滚不会影响 sub2api 服务和账号数据：授予的全是读权限，视图是独立新建对象，sub2api 不感知它的存在。

## 只升级监控（不动 sub2api）

监控容器与 sub2api 完全独立，重建它不会中断网关流量：

```bash
cd /home/work/gpt-monitor
docker compose build
docker compose up -d
curl -fsS http://127.0.0.1:8095/healthz
docker compose logs --tail=30 gpt-pro-monitor
```

打票看板的 DB 预算旋钮在 `.env`（`CODEX_*` 与 `DB_*`，见 `.env.example`）。
临时关掉整个打票看板：把 `CODEX_DASHBOARD_ENABLED` 设为 `false` 再重建容器，相关路由会返回 404，前端也不再显示这个标签页。

## 智商检测

需要 sub2api ≥ `lmuai-0.2.13.2`（lumai-beijing/sub2api#816）。低于这个版本时监控一题不发，看板显示「等待 sub2api 支持」。

1. 建数据目录，属主给容器里的 nonroot（65532）：

   ```bash
   mkdir -p /home/work/gpt-monitor/data
   sudo chown 65532:65532 /home/work/gpt-monitor/data
   sudo chmod 700 /home/work/gpt-monitor/data
   ```

2. `.env` 追加（键名见 `.env.example`「智商检测」段）：
   - `IQ_ENABLED=true`
   - `SUB2API_BASE_URL=http://<当前活跃槽位容器名>:8080`（蓝绿切换后要跟着改，再重建监控容器）
   - `SUB2API_ADMIN_API_KEY=<sub2api 管理员 API Key>`：全权凭证，`.env` 必须是 600

3. 重建容器后看日志里有 `iq probe enabled`，约 5 分钟后出现第一批 `iq probe`。

定时开关、每轮账号数（比例或个数）、检测周期与随机范围、测试模型都在看板「智商检测 → 设置」里改，存在 SQLite 里，保存即生效，不用重建容器。`.env` 里的 `IQ_INTERVAL` / `IQ_JITTER` 只是界面上从没改过时的默认值。完全关掉整个模块就把 `IQ_ENABLED` 设成 `false` 再重建，相关路由会返回 404、标签页隐藏，SQLite 里的历史保留。

## 5. DNS 就绪后启用 Caddy

先确认 `gpt-monitor.lmu.ai` 的权威 DNS 已指向 205 服务器（43.106.104.205），再执行：

```bash
sudo ./deploy/enable-caddy.sh
curl -fsS https://gpt-monitor.lmu.ai/healthz
```

脚本会先备份、校验配置，失败时自动恢复原 Caddyfile。
