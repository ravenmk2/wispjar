# 数据模型（MySQL 8）

> 本文是数据库的权威定义：DDL 可直接执行，迁移文件按此生成。
> 全局约定：
> - 表名一律**单数**（`session_event`、`agent_subagent` …）。
> - 主键：`CHAR(32)`，UUIDv7 去掉 `-` 的 32 位小写 hex，**应用层生成**（`internal/id`），时序有序，天然可作分页游标。
> - 字符集：`utf8mb4 / utf8mb4_unicode_ci`；引擎 InnoDB；时区 `+08:00`（连接 DSN 指定），时间列 `DATETIME(3)` 存本地时间。
> - 软删除用 `status` 字段表达，不做物理删除（`session_event` 除外：它本来就不允许删改）。
> - 迁移：golang-migrate，SQL 文件存 `internal/store/migrations/`，go:embed 进二进制，启动时自动 `up`。

## 0. 列 vs JSON 的取舍约定（避免频繁改表结构）

- 用于 **WHERE / JOIN / ORDER BY / 唯一约束** 的字段必须是独立列（org_id、status、role、name、seq …）。
- **纯配置、展示、扩展性字段优先进 JSON 列**：`model_provider.extra`、`model.capabilities`、`model.default_params`、`agent.model_params`、`agent.tools_enabled`、`agent.sandbox_config`、`session.agent_snapshot`、`session.sandbox`、`session.usage_total`、`session_event.data`、`platform_setting.value`、`audit_log.detail`。
- **未来新增配置项默认并入既有 JSON 列**（如新增沙箱旋钮 → `agent.sandbox_config`），只有需要查询/关联时才新增独立列并写 migration。

## 1. 迁移与种子

- 迁移文件命名：`000001_init.up.sql` / `000001_init.down.sql` 递增。
- 种子数据在 `000001_init.up.sql` 内完成（见 §11）：平台管理机构、默认平台设置。
- 初始超管：用户名 `admin`，由应用启动时播种（bcrypt 需应用层计算）：初始密码取环境变量 `WISPJAR_ADMIN_PASSWORD`，未提供则生成随机值打印到启动日志；`must_change_password=1` 强制首登改密。

## 2. organization（机构）

```sql
CREATE TABLE organization (
  id          CHAR(32)     NOT NULL PRIMARY KEY,
  name        VARCHAR(128) NOT NULL,
  is_platform TINYINT(1)   NOT NULL DEFAULT 0,          -- 1=内置平台管理机构，全局唯一
  status      VARCHAR(16)  NOT NULL DEFAULT 'active',   -- active / disabled
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

"仅一行 is_platform=1"用生成列表达（MySQL 唯一索引中多行 NULL 不冲突）：

```sql
ALTER TABLE organization
  ADD COLUMN platform_guard CHAR(1) GENERATED ALWAYS AS (IF(is_platform=1,'1',NULL)) VIRTUAL,
  ADD UNIQUE KEY uk_org_platform_guard (platform_guard);
```

## 3. user（用户）

```sql
CREATE TABLE user (
  id                   CHAR(32)     NOT NULL PRIMARY KEY,
  username             VARCHAR(64)  NOT NULL,
  password_hash        VARCHAR(128) NOT NULL,           -- bcrypt，cost=12
  display_name         VARCHAR(128) NOT NULL DEFAULT '',
  status               VARCHAR(16)  NOT NULL DEFAULT 'active',  -- active / disabled
  must_change_password TINYINT(1)   NOT NULL DEFAULT 0,
  created_at           DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at           DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_user_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

## 4. org_member（机构成员）

```sql
CREATE TABLE org_member (
  id         CHAR(32)    NOT NULL PRIMARY KEY,
  org_id     CHAR(32)    NOT NULL,
  user_id    CHAR(32)    NOT NULL,
  role       VARCHAR(16) NOT NULL,                      -- admin / member
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_member_org_user (org_id, user_id),
  KEY idx_member_user (user_id),
  CONSTRAINT fk_member_org  FOREIGN KEY (org_id)  REFERENCES organization(id),
  CONSTRAINT fk_member_user FOREIGN KEY (user_id) REFERENCES user(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

## 5. model_provider（供应商，平台级）

```sql
CREATE TABLE model_provider (
  id          CHAR(32)     NOT NULL PRIMARY KEY,
  name        VARCHAR(128) NOT NULL,                    -- 展示名，如 "DeepSeek 官方"
  type        VARCHAR(32)  NOT NULL,                    -- deepseek / ark / qwen / openai_compat
  base_url    VARCHAR(512) NOT NULL,
  api_key_enc BLOB         NOT NULL,                    -- AES-256-GCM：nonce(12B) || ciphertext
  extra       JSON         NULL,                        -- {"remark":"...","last_check":{"ok":true,"at":"...","latency_ms":0}}
  status      VARCHAR(16)  NOT NULL DEFAULT 'active',   -- active / disabled
  created_by  CHAR(32)     NOT NULL,
  created_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_provider_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

加密约定（`internal/provider/crypto.go`）：密钥明文只在内存出现；`api_key_enc = nonce(12字节) || AES-256-GCM(key=master_key, nonce, plaintext)`；master key 为 32 字节，来自环境变量 `WISPJAR_MASTER_KEY`（base64 或 hex 编码，启动时解码校验长度）。备注、连通性检查结果等扩展信息一律放 `extra`，不加列。

## 6. model（上架模型，平台级）

```sql
CREATE TABLE model (
  id             CHAR(32)     NOT NULL PRIMARY KEY,
  provider_id    CHAR(32)     NOT NULL,
  model_code     VARCHAR(128) NOT NULL,                 -- 调 API 的模型名：deepseek-chat / doubao-pro-32k / qwen-max
  display_name   VARCHAR(128) NOT NULL,
  capabilities   JSON         NOT NULL,                 -- {"tools":true,"reasoning":true,"vision":false,"streaming":true}
  context_size   INT          NOT NULL DEFAULT 64000,   -- tokens
  default_params JSON         NULL,                     -- {"temperature":0.7,"max_tokens":4096,...}
  status         VARCHAR(16)  NOT NULL DEFAULT 'active',
  created_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_model_provider_code (provider_id, model_code),
  CONSTRAINT fk_model_provider FOREIGN KEY (provider_id) REFERENCES model_provider(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

## 7. platform_setting（平台设置 KV）

```sql
CREATE TABLE platform_setting (
  `key`      VARCHAR(64) NOT NULL PRIMARY KEY,
  value      JSON        NOT NULL,
  updated_by CHAR(32)    NOT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

已定义的 key（新平台配置一律新增 key，不改表结构）：

| key | value 结构 | 默认 |
|---|---|---|
| `sandbox.default_image` | `string` | `"python:3.12"`（首次部署后由管理员替换为预装工具镜像，见 runtime.md §7.3） |
| `sandbox.images` | `string[]`（可选镜像列表，Agent 编辑器下拉源） | `["python:3.12"]` |
| `sandbox.egress_policy` | `{defaultAction:"allow"|"deny", rules:[{action:"allow"|"deny", target:string}]}` | `{"defaultAction":"allow","rules":[]}` |
| `sandbox.resource_limits` | `{cpu:string, memory:string}` | `{"cpu":"1","memory":"1Gi"}` |
| `sandbox.idle_ttl_seconds` | `number`（空闲多久回收） | `1800` |
| `sandbox.ttl_seconds` | `number`（沙箱硬 TTL，随活动续约） | `3600` |

## 8. agent / skill / agent_skill / agent_subagent（机构级）

```sql
CREATE TABLE agent (
  id             CHAR(32)      NOT NULL PRIMARY KEY,
  org_id         CHAR(32)      NOT NULL,
  name           VARCHAR(128)  NOT NULL,
  description    VARCHAR(1024) NOT NULL DEFAULT '',     -- task 委派时作为能力描述展示给模型
  avatar         VARCHAR(512)  NOT NULL DEFAULT '',
  system_prompt  MEDIUMTEXT    NOT NULL,
  model_id       CHAR(32)      NOT NULL,
  model_params   JSON          NULL,                    -- 覆盖 model.default_params
  max_iterations INT           NOT NULL DEFAULT 20,
  tools_enabled  JSON          NOT NULL,                -- {"exec":true,"read":true,"write":true,"edit":true,"glob":true,"grep":true}
  sandbox_config JSON          NULL,                    -- {"image":"","resources":{"cpu":"2","memory":"2Gi"}}；空/缺省=平台默认
  status         VARCHAR(16)   NOT NULL DEFAULT 'draft',-- draft / published / archived
  created_by     CHAR(32)      NOT NULL,
  created_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_agent_org_status (org_id, status),
  CONSTRAINT fk_agent_org   FOREIGN KEY (org_id)   REFERENCES organization(id),
  CONSTRAINT fk_agent_model FOREIGN KEY (model_id) REFERENCES model(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE skill (
  id           CHAR(32)      NOT NULL PRIMARY KEY,
  org_id       CHAR(32)      NOT NULL,
  name         VARCHAR(64)   NOT NULL,                  -- SKILL.md frontmatter name（小写+连字符）
  description  VARCHAR(1024) NOT NULL,
  meta         JSON          NOT NULL,                  -- 完整 frontmatter + {"files":["SKILL.md","scripts/a.py",...]}
  storage_path VARCHAR(512)  NOT NULL,                  -- <data_dir>/skills/<org_id>/<skill_id>/
  zip_size     BIGINT        NOT NULL DEFAULT 0,
  status       VARCHAR(16)   NOT NULL DEFAULT 'active', -- active / disabled
  created_by   CHAR(32)      NOT NULL,
  created_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at   DATETIME(3)   NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_skill_org_name (org_id, name),
  CONSTRAINT fk_skill_org FOREIGN KEY (org_id) REFERENCES organization(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent_skill (
  agent_id CHAR(32) NOT NULL,
  skill_id CHAR(32) NOT NULL,
  sort     INT      NOT NULL DEFAULT 0,                 -- 提示词索引段中的展示顺序
  PRIMARY KEY (agent_id, skill_id),
  CONSTRAINT fk_askill_agent FOREIGN KEY (agent_id) REFERENCES agent(id) ON DELETE CASCADE,
  CONSTRAINT fk_askill_skill FOREIGN KEY (skill_id) REFERENCES skill(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent_subagent (
  agent_id     CHAR(32) NOT NULL,
  sub_agent_id CHAR(32) NOT NULL,
  sort         INT      NOT NULL DEFAULT 0,
  PRIMARY KEY (agent_id, sub_agent_id),
  CONSTRAINT fk_asub_agent FOREIGN KEY (agent_id)     REFERENCES agent(id) ON DELETE CASCADE,
  CONSTRAINT fk_asub_sub   FOREIGN KEY (sub_agent_id) REFERENCES agent(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

`agent_subagent` 业务约束（service 层校验，DB 不表达）：同机构；`sub_agent_id != agent_id`；保存时做环检测（限 1 层深度：子 Agent 自身不得再出现在任何 agent_subagent 的 agent_id 列——即子 Agent 不能再有下级）；仅 published Agent 可被委派（被引用者下线时，主 Agent 的 task 工具描述中将其移除并记审计）。

## 9. session / session_event / agent_checkpoint（核心）

```sql
CREATE TABLE session (
  id             CHAR(32)     NOT NULL PRIMARY KEY,
  org_id         CHAR(32)     NOT NULL,
  user_id        CHAR(32)     NOT NULL,
  agent_id       CHAR(32)     NOT NULL,
  agent_snapshot JSON         NOT NULL,                 -- 创建时 Agent 全量配置快照（含 skills/subagents 解析结果）
  title          VARCHAR(256) NOT NULL DEFAULT '',
  title_pinned   TINYINT(1)   NOT NULL DEFAULT 0,
  approval_mode  VARCHAR(16)  NOT NULL DEFAULT 'default', -- strict / default / auto
  model_id       CHAR(32)     NOT NULL,                 -- 会话级模型（创建时=agent.model_id，可切换）
  sandbox        JSON         NOT NULL,                 -- {"id":"","status":"none"}；status: none/creating/running/recycled/failed
  last_seq       BIGINT       NOT NULL DEFAULT 0,       -- 事件投影游标
  running        TINYINT(1)   NOT NULL DEFAULT 0,
  usage_total    JSON         NOT NULL,                 -- {"input_tokens":0,"output_tokens":0,"cache_read_tokens":0,"cache_write_tokens":0,"reasoning_tokens":0}
  status         VARCHAR(16)  NOT NULL DEFAULT 'active',    -- active / archived / deleted
  last_active_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_session_user (org_id, user_id, status, last_active_at),
  CONSTRAINT fk_session_org   FOREIGN KEY (org_id)   REFERENCES organization(id),
  CONSTRAINT fk_session_user  FOREIGN KEY (user_id)  REFERENCES user(id),
  CONSTRAINT fk_session_agent FOREIGN KEY (agent_id) REFERENCES agent(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE session_event (
  session_id CHAR(32)    NOT NULL,
  seq        BIGINT      NOT NULL,                      -- 会话内单调连续，从 1 开始
  type       VARCHAR(64) NOT NULL,                      -- 见 protocol.md §6 事件词表
  time_ms    BIGINT      NOT NULL,                      -- epoch ms
  data       JSON        NOT NULL,
  PRIMARY KEY (session_id, seq),
  CONSTRAINT fk_event_session FOREIGN KEY (session_id) REFERENCES session(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent_checkpoint (
  id         CHAR(32)    NOT NULL PRIMARY KEY,          -- checkpoint id
  session_id CHAR(32)    NOT NULL,
  data       MEDIUMBLOB  NOT NULL,                      -- gob 序列化的 adk 运行状态
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_checkpoint_session (session_id),
  CONSTRAINT fk_checkpoint_session FOREIGN KEY (session_id) REFERENCES session(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**写入规则**：

1. 追加事件：`INSERT session_event` + `UPDATE session SET last_seq=?, running=?, usage_total=?, last_active_at=?` 在**同一事务**。
2. `seq` 分配：读取 `session.last_seq`（`SELECT ... FOR UPDATE` 行锁）后 +1；一个会话同一时刻只有一个写者（runtime 按 session_id 的内存互斥锁保证）。
3. `session_event` 永不 UPDATE/DELETE；上下文压缩只追加新事件（`surface_op` 语义在 protocol.md §6.3）。
4. 回放：`SELECT seq,type,time_ms,data FROM session_event WHERE session_id=? AND seq>? ORDER BY seq`。
5. `agent_checkpoint` 定期清理：`DELETE WHERE created_at < NOW() - INTERVAL 7 DAY`（审批 checkpoint 在 decided 后立即删除）。
6. 沙箱状态读写一律经 `session.sandbox` JSON（`JSON_SET` / 应用层整体改写均可）；崩溃恢复时 `WHERE JSON_UNQUOTE(JSON_EXTRACT(sandbox,'$.status'))='running'` 仅在启动扫描执行一次，无需独立列。

## 10. usage_daily / audit_log

```sql
CREATE TABLE usage_daily (
  date               DATE     NOT NULL,
  org_id             CHAR(32) NOT NULL,
  user_id            CHAR(32) NOT NULL,
  agent_id           CHAR(32) NOT NULL,
  model_id           CHAR(32) NOT NULL,
  requests           INT      NOT NULL DEFAULT 0,       -- 模型请求次数（每个 step 一次）
  input_tokens       BIGINT   NOT NULL DEFAULT 0,
  output_tokens      BIGINT   NOT NULL DEFAULT 0,
  cache_read_tokens  BIGINT   NOT NULL DEFAULT 0,
  cache_write_tokens BIGINT   NOT NULL DEFAULT 0,
  reasoning_tokens   BIGINT   NOT NULL DEFAULT 0,
  PRIMARY KEY (date, org_id, user_id, agent_id, model_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE audit_log (
  id         CHAR(32)    NOT NULL PRIMARY KEY,
  org_id     CHAR(32)    NOT NULL DEFAULT '',           -- 平台级操作为空串
  actor_id   CHAR(32)    NOT NULL,
  action     VARCHAR(64) NOT NULL,                      -- provider.create / provider.update_key / agent.publish / member.add / skill.upload ...
  target     VARCHAR(128) NOT NULL DEFAULT '',
  detail     JSON        NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_audit_org_time (org_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

`usage_daily` 聚合方式：runtime 在每个 `assistant/message` 事件提交后，从事件 `data.usage` 异步 `INSERT ... ON DUPLICATE KEY UPDATE ... = ... + VALUES(...)`（按事件发生日的本地日期）。子 Agent 的用量记在主 Agent 行上（事件中保留 `sub_agent_id` 供明细分析，报表 v1 不展示）。

## 11. 种子数据（000001_init.up.sql 尾部）

```sql
-- 平台管理机构（固定 id 便于引用：全 0 变体不符合 UUIDv7，仅作内置常量，见 internal/id/wellknown.go）
INSERT INTO organization (id, name, is_platform) VALUES
  ('00000000000000000000000000000001', '平台管理机构', 1);

-- 默认平台设置
INSERT INTO platform_setting (`key`, value, updated_by) VALUES
  ('sandbox.default_image',    '"python:3.12"',                'system'),
  ('sandbox.images',           '["python:3.12"]',              'system'),
  ('sandbox.egress_policy',    '{"defaultAction":"allow","rules":[]}', 'system'),
  ('sandbox.resource_limits',  '{"cpu":"1","memory":"1Gi"}',   'system'),
  ('sandbox.idle_ttl_seconds', '1800',                         'system'),
  ('sandbox.ttl_seconds',      '3600',                         'system');
```

## 12. 库外文件存储

| 内容 | 位置 | 说明 |
|---|---|---|
| Skill zip 解压目录 | `<data_dir>/skills/<org_id>/<skill_id>/` | 原始 zip 保留为 `_source.zip` 便于重新校验；路径配置化，保留对象存储扩展口 |
| 沙箱内文件 | 沙箱容器内 | 经 execd 上传/下载；沙箱回收即销毁 |
