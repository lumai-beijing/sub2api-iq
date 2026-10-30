# GPT-Pro Monitor

只读监控 sub2api 中 OpenAI OAuth 账号的两块内容：**续费看板**（套餐到期、账号状态、Codex 用量快照）和 **292 打票看板**（turn-state 票池健康度、打票效果、降智率、负载与容量）。

## 能力

### 续费看板

- 套餐到期分级：已过期、3/7/14/30 天内。
- 账号状态与调度状态分离展示。
- 账号 `expires_at` 早于套餐到期时标记冲突。
- 展示 5h/7d 用量、重置时间和快照新鲜度。

### 292 打票看板

回答两个运营问题：**要不要调打票配置**、**要不要补号**。

- **多门控模型**：门控清单直接读 sub2api 的 `openai_codex_ticket_models`（清单变化 ≤60s 跟上）。每个模型一张卡：持票覆盖、最薄池的地板余量、当前降智率、采集速率；票池 / 打票 / 降智三个维度按模型切换，账号表每个模型一列票状态。
- **结论区**：按模型分别给出（余量跌破地板、覆盖率过低、降智率异常），容量 / 到期类只给一份；按严重度排序放在最上面。
- **票池健康度**：持票 / 可调度覆盖率、新鲜票与临期票、分池余量对比 `fail_closed` 地板、持票账号数趋势（按模型分线）。零持票的池标「未启动」而不是「跌破」——刚加进清单的模型会经历这个阶段。
- **292 打票效果**：在手票数、采集速率与配置上限、平均尝试次数、在手票的采集时刻分布。
- **降智直接效果**：降智判据与 sub2api 看门狗（PR #718）同一套谱系规则——上游返回跨系列或更低版本才算降智，同系列升版（`gpt-5.6-sol → gpt-6-sol`）单列不计入。展示当前降智率（近 3 小时）、上游实际返回构成、逐小时请求量与降智率、按天趋势（按需加载）。
- **负载与容量**：RPM、平均/峰值并发、单号 RPM、7 天额度剩余率分布、套餐到期统计。
- **当前生效配置**：区分「settings 表显式配过」与「回落到代码内置默认」，并标出哪些能后台热改、哪些要重建容器。
- **账号明细**：按票状态、分组、关键字筛选。

### 智商检测

回答：**哪些号在「同一个模型名」下答题水平变差了**。降智看板只看得到上游回了哪个模型名，看不到模型名没变、答案却错了的情况。

- 定时一轮，从此刻可调度的 OpenAI OAuth 号里抽一部分，越久没测过的号越优先；一个周期内测过的号这轮跳过。默认每 5 分钟 ±60 秒、抽 10%（至少 2 个），和上线时的行为一致。
- 定时检测在看板「设置」里配，保存即生效、不用重启：
  - **开关**：关掉后不再定时发题，手动「立即跑一轮 / 立即检测」照常可用。
  - **每轮账号**：按比例（可用号的百分比，向上取整，带下限）或按固定个数，都不超过可用总数。
  - **锚定周期 + 随机范围**：锚点对齐到周期的整数倍（5 分钟周期 = :00 :05 :10 …，1 小时 = 每个整点），每轮在锚点 ± 随机范围内开跑，不受上一轮耗时影响；随机范围最多是周期的一半。上一轮超时错过了整个随机窗口时直接跳过该周期，不补跑。
- 题目是一道有标准答案的糖果题（答案 21），**要求只输出 `{"answer": <整数>, "reasoning": "..."}`**：按 `answer` 字段判分；没给 JSON 时退回取正文最后一个数字，看板单列 JSON 作答率。
- **题面每次随机改写**（`internal/iq/variant.go`）：防上游对同一道题做问答缓存、或认出检测题后换更强的模型答。只换叙述方式——措辞与同义词（个/颗/块、糖/糖果、味/口味）、数量的写法（行内、分条、Markdown 表格、句子）、口味的列举顺序、形状先后、阿拉伯数字或汉字、段落排布、开头引语、作答要求的说法，约 2.4 万亿种组合；所有条件（黑色袋子、形状靠手感能分辨、摸之前先定数目、两种满足方式的说明）、六个数量、问法和答案都不变，也不加任何提示。每个槽位的第 0 项拼起来逐字等于原题，5000 个随机题面逐条核对条件和数量都由测试守着。每条记录存下当次题面，采样详情里能看到。
- 随机化之前的记录题目 ID 是 `candy-json-v1`（固定题面），之后是 `candy-json-v2`；统计默认合在一起，也可以只看其中一种。某个来源随机化后答对率显著下降（两边各至少 20 次、差 ≥15 个百分点且 z 检验显著）时，结论里会点名「可能来自问答缓存」。
- 经 sub2api 管理员测试接口（`POST /api/v1/admin/accounts/:id/test`，lumai-beijing/sub2api#816）发题：指定账号、带推理强度、门控模型按网关同一套规则注入 292 票；`probe` 模式下成功不清账号的冷却状态、不写审计。
- 测试模型在看板「设置」里配（`模型@推理强度`，可多个），每个抽中的号随机分一个。
- **分组检测**：在「分组」里勾选要横向对比的 sub2api OpenAI 分组（列表带成员构成、此刻可用数、近 24 小时请求数、答对率）。分组的智商 = **检测时属于这个分组的号**的答题结果：号池每轮照常按比例抽号，抽完后给每个勾选的分组补测到至少 N 个号（默认 2，组里的 API Key 号也会测到），测试模型与号池相同。每条记录存下检测时号所属的全部分组（`iq_probe_groups`），号以后换了分组也不影响已有记录。测的是号本身（经测试接口直连上游），不含网关的分组级设置（推理强度上限、模型映射）；要测用户经网关实际拿到的效果，可以把网关地址和该分组的 API Key 加成一个第三方。
- 「号池整体」只算按比例抽样的 OAuth 号：为分组补测的、API Key 号都不算，免得被选中的分组把整体口径带偏。
- **总览矩阵**是首屏：行 = 号池整体、选中的分组、第三方供应商，列 = 测试模型。每格是最近 30 次可判分的答对率（判级口径）和迷你红绿条，标出同列最高、与号池整体的差；可切换成 24 小时 / 7 / 30 天 / 全部的合计做长期横比，也可以只看随机题面或固定题面。点格子展开详情：完整历史条（固定题面的点画浅）和最近 20 次的答案、推理要点、当次题面、所在分组。
- **结论**在矩阵上方，按严重程度列出：哪个来源降智 / 偏低（一个来源几个模型合成一条，附最常见错答的含义——29 是没用上「形状能摸出来」、按盲摸算的最坏情况）、出错太多、明显比号池好或差（统计显著才说）、题面随机化前后的变化、样本不足、分组没有可用号或已删除。点结论直接打开对应的格子。
- 每个模型一张卡（点开矩阵的格子看到）：状态徽章（最近 30 次正确率 ≥80% 正常、<50% 降智）、正确率、平均耗时、换模型次数、最近 180 次的红绿历史条。
- 账号排行（24h / 7d / 30d / 全部，可按分组筛）：准确率从低到高，样本少于 5 次标「样本不足」不参与排名，每个号显示当前所在的分组；采样详情可按来源（号池整体 / 全部号 / 某个分组 / 某个第三方）、结果、题面筛选，能看到每次的答案、推理要点、题面、原文、上游模型、是否注入了票。
- 出错（上游 429、超时等）不计入准确率分母：它说明号或代理坏了，不是智商低。
- **第三方供应商**：「第三方」里添加任意 OpenAI 兼容接口（Base URL + API Key，Chat Completions 或 Responses 格式），每个供应商**独立**配置检测周期（同样是锚定周期 ± 随机）、测试模型、每个模型每轮测几次，不影响号池。添加前可「测试连接」（调一次 `GET /models`，并提示测试模型在不在对方的列表里）。
- **每次检测都是新会话**：中转号池的粘性路由会把同一会话固定在一个上游账号上（常见 TTL 1 小时、命中续期），每几分钟发同一道题会一直落在同一个号。所以每次检测都带一个随机 UUID 作会话 ID（请求头 `session_id` / `Session-Id` / `X-Session-Id` / `conversation_id`，Responses 格式另带请求体 `prompt_cache_key`）；题面本身每次随机改写，中转忽略会话头、按「模型 + 首条用户消息」算哈希时也每次不同。
- 第三方和号池、分组在总览矩阵里按「模型@推理强度」对齐比较；模型名和推理强度要和号池一致才会落在同一列。

约束：

- 续费看板与打票看板**只读**：不调用上游，不写 sub2api 的库。
- 智商检测是唯一会调用上游的模块：号池按配置抽号各答一道题；第三方供应商按各自配置发题。结果写在监控**自己的** SQLite（`/data/iq.db`，默认保留 90 天），不写 sub2api 的库。sub2api 低于 `IQ_MIN_SUB2API_VERSION` 时号池一题不发（旧接口会把题目换成 "hi"），第三方不受影响。
- 第三方 API Key 用 AES-GCM 加密后存进 SQLite，页面只显示末 4 位；发题时不跟随重定向、默认拒绝内网目标，Key 不进日志和错误信息（都有测试守着）。
- 持票数趋势由监控进程自己在内存里采样（票在 `accounts.extra` 里是覆盖写的，数据库没有历史），重启后重新累积。
- `turn-state` 票本身是路由凭证，解析后立刻丢弃，绝不出现在 API 响应里；sub2api 管理员 Key 只在 `.env` 里，不进响应与日志（都有测试守着）。

独立管理员登录，5 分钟浏览器自动刷新。

## 对数据库的压力

46 上 `usage_logs` 是 5.6GB / 410 万行，所以查询是按「可测量的预算」设计的，实测（2026-09-20，46 生产库）：

| 查询 | 触发频率 | 耗时 | 磁盘读 |
| --- | --- | --- | --- |
| 票池（`accounts` + 分组） | 每 60s 最多一次 | 15.5ms | 0（全缓存命中） |
| 用量聚合（24h 单遍扫描，按 请求模型→返回模型 成对） | 每 120s 最多一次 | 1 模型 18–35ms / 2 模型 ~96ms | 0–5MB |
| 7 天按天趋势 | 仅前端点击，缓存 30 分钟 | 370ms | ~280MB |
| 智商检测：可用号与分组成员（`accounts` + `account_groups` + `groups`） | 每轮一次 + 看板刷新时 | 毫秒级 | 0 |
| 智商检测：分组近 24h 请求数（`usage_logs`） | 每 10 分钟最多一次 | 8ms（147，3 万行，走 `idx_usage_logs_group_created_at_not_null` 只读索引） | ~1.6MB |

稳态（没人点 7 天趋势）约每小时 1.9 秒数据库时间，约等于单核的 0.05%。

保证这个预算不被突破的几处设计：

- **一次请求只扫一遍 `usage_logs`**：逐账号、逐小时、总计三层聚合用 `GROUPING SETS` 一次算完，而不是分三条查询。
- **只走 `idx_usage_logs_effective_requested_model_created`**：谓词必须写成 `coalesce(nullif(btrim(requested_model),''), model) = ANY($models)`，改写任何一处都会退化成全表扫。多模型走 Bitmap 索引扫描，成本随模型数近似线性。
- **降智裁决不写在 SQL 里**：SQL 只按（请求模型, 返回模型）成对计数，谱系判定在 Go 里做，这样规则与 sub2api 看门狗保持一处口径，也不用为每个新模型改查询。
- **窗口上限硬编码 24 小时**：7 天要读约 280MB 堆页，会把 sub2api 的热缓存挤掉，所以只给按需面板用，绝不进自动刷新路径。
- **三条线各自独立缓存 + 各一把单飞锁**：并发请求只打一次库。
- **刷新失败回落到上一份好数据**并打 `freshness.degraded` 标记，不会因为失败而反复重试打库。
- **连接级护栏**：`statement_timeout=8s`、`MaxConns=4`、`application_name=gpt-pro-monitor`（在 `pg_stat_activity` 里可直接认出）。

## 本地开发

要求 Go 1.27、Node.js 22+。

```bash
cp .env.example .env
# 编辑 .env，至少设置 DATABASE_URL、管理员密码哈希和 SESSION_SECRET
printf '%s' 'your-strong-password' | go run ./cmd/server hash-password
```

启动前端开发服务器：

```bash
cd frontend
npm install
npm run dev
```

启动 Go 服务：

```bash
go run ./cmd/server
```

Vite 开发服务器需要把 `/api` 和 `/healthz` 代理到 `http://127.0.0.1:8080`。生产构建会将前端嵌入 Go 二进制：

```bash
cd frontend && npm ci && npm run build
cd .. && go build ./cmd/server
```

## 测试

```bash
go test ./...
cd frontend && npm test
```

## API

- `POST /api/login`
- `POST /api/logout`
- `GET /api/session`
- `GET /api/v1/gpt-pro/accounts`
- `GET /api/v1/features`
- `GET /api/v1/codex-ticket/overview`
- `GET /api/v1/codex-ticket/daily-trend`（代价高，前端按需触发）
- `GET /api/v1/iq/overview`（含默认口径的总览矩阵与结论）
- `GET /api/v1/iq/board?window=recent|24h|7d|30d|all&scope=all|random|fixed`（总览矩阵）、`GET /api/v1/iq/card?source=pool|group:<id>|provider:<id>&model=&scope=`（单格详情）
- `GET /api/v1/iq/groups?window=`（全部 OpenAI 分组与检测状态）
- `GET /api/v1/iq/accounts?window=24h|7d|30d|all&group_id=`、`GET /api/v1/iq/probes?source=pool|accounts|group:<id>|provider:<id>&account_id=&model=&outcome=&scope=&limit=`
- `POST /api/v1/iq/run`（立即检测；body 可带 `account_id`（单号）或 `group_id`（只测这个分组），以及 `model`；后台执行，20 秒冷却）
- `GET|PUT /api/v1/iq/settings`（测试模型清单；定时配置 `schedule: {enabled, interval_seconds, jitter_seconds, sample_mode: ratio|count, sample_percent, sample_min, sample_count}`；分组 `groups: {ids, per_round}`；都可只传要改的字段）
- `GET|POST /api/v1/iq/providers`、`PUT|DELETE /api/v1/iq/providers/{id}`（`DELETE` 带 `?keep_probes=true` 保留检测记录）
- `POST /api/v1/iq/providers/test`（测试连接：调一次 `GET /models`）、`POST /api/v1/iq/providers/{id}/run`（立即检测，body 可带 `model`）
- `GET /api/v1/iq/probes?provider_id=`（旧参数，等于 `source=provider:<id>`）
- `GET /healthz`

除登录、会话和健康检查外，监控数据接口需要有效会话 Cookie；智商检测的两个写接口还要求同源。

## 安全边界

- 数据库账号只有读权限，且按最小面授予：`accounts` 整表 `SELECT`；`usage_logs`、`groups`、`account_groups` 只给列级 `SELECT`（`user_id` / `ip_address` / `user_agent` 等 PII 列不授予）；打票配置走只读视图 `gpt_monitor_codex_settings`，因为 `settings` 表里有明文 `admin_api_key`。`deploy/setup-readonly-role.sh` 跑完会自动自检这两个敏感面确实读不到。
- 登录密码只保存 bcrypt 哈希，明文不写入配置或日志。
- 会话 Cookie 为 HttpOnly、SameSite=Lax，生产环境必须启用 Secure。
- 容器以非 root 用户运行、只读根文件系统、移除 Linux capabilities。

## 本地 Docker 连接生产只读快照

本地 Docker 无法直接访问 205 上的容器内网，项目提供 SSH 隧道脚本：

```bash
cp .env.example .env.local
# 使用生产只读角色的 DATABASE_URL，主机写 host.docker.internal，端口写 15432
./scripts/start-local.sh
```

启动后访问 `http://localhost:18095`。停止：

```bash
./scripts/stop-local.sh
```
