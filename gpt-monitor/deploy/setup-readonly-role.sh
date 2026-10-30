#!/usr/bin/env bash
set -euo pipefail

# 幂等：创建/更新只读角色 gpt_monitor_ro，并授予监控所需的最小读权限。
# 全程只做 GRANT 与一个只读视图，不改 sub2api 的任何表结构或数据。

PG_CONTAINER="${PG_CONTAINER:-sub2api-postgres}"
PG_DATABASE="${PG_DATABASE:-sub2api}"
PG_ADMIN_USER="${PG_ADMIN_USER:-sub2api}"
ROLE_NAME="${MONITOR_DB_USER:-gpt_monitor_ro}"
ROLE_PASSWORD="${MONITOR_DB_PASSWORD:?MONITOR_DB_PASSWORD is required}"

docker exec -i "$PG_CONTAINER" psql \
  -v ON_ERROR_STOP=1 \
  -U "$PG_ADMIN_USER" \
  -d "$PG_DATABASE" \
  --set=role_name="$ROLE_NAME" \
  --set=role_password="$ROLE_PASSWORD" <<'SQL'
BEGIN;

SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'role_name', :'role_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'role_name')
\gexec

ALTER ROLE :"role_name" WITH LOGIN PASSWORD :'role_password';
GRANT CONNECT ON DATABASE :"DBNAME" TO :"role_name";
GRANT USAGE ON SCHEMA public TO :"role_name";

-- 续费看板：沿用整表 SELECT。
-- 注意 credentials 列含 OAuth refresh token —— 邮箱/套餐到期也在这个列里，
-- 所以无法用列级权限再收窄。要彻底去掉这份暴露只能另建账号视图（见 deploy/README.md）。
GRANT SELECT ON TABLE accounts TO :"role_name";

-- 292 打票看板：列级授权，PII 列（user_id / ip_address / user_agent 等）一律不给。
GRANT SELECT (
	id, account_id, group_id, model, requested_model, upstream_response_model,
	created_at, duration_ms, stream, input_tokens, output_tokens, cache_read_tokens
) ON TABLE usage_logs TO :"role_name";

GRANT SELECT (id, name, platform, status, deleted_at) ON TABLE groups TO :"role_name";
GRANT SELECT (account_id, group_id) ON TABLE account_groups TO :"role_name";

-- settings 表里有明文 admin_api_key，绝不能整表放出去。
-- 这个视图只暴露 292 打票相关的键，且把可能含凭据的代理 URL 脱敏成 [set]。
-- 视图默认以属主权限执行，所以角色不需要 settings 本表的任何权限。
CREATE OR REPLACE VIEW gpt_monitor_codex_settings AS
SELECT
	key,
	CASE
		WHEN key LIKE '%proxy_url%'
			THEN CASE WHEN btrim(COALESCE(value, '')) = '' THEN '' ELSE '[set]' END
		ELSE value
	END AS value
FROM settings
WHERE key LIKE 'openai_codex_ticket%';

GRANT SELECT ON gpt_monitor_codex_settings TO :"role_name";

COMMIT;
SQL

echo "Read-only role $ROLE_NAME is configured."

# 自检：确认敏感面真的够不着。
docker exec -i "$PG_CONTAINER" psql -v ON_ERROR_STOP=1 -U "$PG_ADMIN_USER" -d "$PG_DATABASE" \
  --set=role_name="$ROLE_NAME" <<'SQL'
SET ROLE :"role_name";
DO $$
BEGIN
	PERFORM 1 FROM settings WHERE key = 'admin_api_key';
	RAISE EXCEPTION '自检失败：角色仍可读 settings 全表';
EXCEPTION WHEN insufficient_privilege THEN
	RAISE NOTICE '自检通过：settings 全表不可读';
END $$;
DO $$
BEGIN
	PERFORM ip_address FROM usage_logs LIMIT 1;
	RAISE EXCEPTION '自检失败：角色仍可读 usage_logs.ip_address';
EXCEPTION WHEN insufficient_privilege THEN
	RAISE NOTICE '自检通过：usage_logs 的 PII 列不可读';
END $$;
SELECT count(*) AS codex_settings_visible FROM gpt_monitor_codex_settings;
RESET ROLE;
SQL
