# 数据模型（MySQL 8）

> 本文是数据库的权威定义：DDL 可直接执行，迁移文件按此生成。
> 云电脑增量模型见 [computers.md §5](computers.md#5-数据模型)。七张新表、session 电脑引用与 skill 修订字段见 §14（000005 增量迁移）；000008 删除旧会话及 sandbox 字段，并将 session.computer_id 设为 NOT NULL；共享电脑状态以 computer 为准。
> 全局约定：
> - 表名一律**单数**（`session_event`、`agent_subagent` …）。
> - 主键：`CHAR(32)`，UUIDv7 去掉 `-` 的 32 位小写 hex，**应用层生成**（`internal/id`），时序有序，天然可作分页游标。
> - 字符集：`utf8mb4 / utf8mb4_unicode_ci`；引擎 InnoDB；时区 `+08:00`（连接 DSN 指定），时间列 `DATETIME(3)` 存本地时间。
> - 软删除用 `status` 字段表达，不做物理删除（`session_event` 除外：它本来就不允许删改）。
> - 迁移：golang-migrate，SQL 文件存 `internal/store/migrations/`，go:embed 进二进制，启动时自动 `up`。

## 0. 列 vs JSON 的取舍约定（避免频繁改表结构）

- 用于 **WHERE / JOIN / ORDER BY / 唯一约束** 的字段必须是独立列（org_id、status、role、name、seq …）。
- **纯配置、展示、扩展性字段优先进 JSON 列**：`model_provider.extra`、`model.capabilities`、`model.default_params`、`agent.model_params`、`agent.tools_enabled`、`agent.sandbox_config`、`session.agent_snapshot`、`session.model_params`、`session.usage_total`、`session_event.data`、`platform_setting.value`、`audit_log.detail`。
- **未来新增配置项默认并入既有 JSON 列**（如新增沙箱旋钮 → `agent.sandbox_config`），只有需要查询/关联时才新增独立列并写 migration。

## 1. 迁移与种子

- 迁移文件命名：`000001_init.up.sql` / `000001_init.down.sql` 递增。
- 种子数据在 `000001_init.up.sql` 内完成（见 §12），内置组织通过 `000007_system_display_names.up.sql` 统一默认显示名为「系统管理」。历史迁移保持原样。
- 初始系统管理员：用户名 `admin`，由应用启动时播种（bcrypt 需应用层计算）：初始密码取环境变量 `WISPJAR_ADMIN_PASSWORD`，未提供则生成随机值打印到启动日志；`must_change_password=1` 强制首登改密。
- 默认显示名迁移：`000007_system_display_names.up.sql` 仅把固定内置组织且名称仍为旧默认值的记录改为「系统管理」；仅把用户名为 `admin`、拥有内置组织管理员成员关系且显示名仍为旧默认值的账号改为「系统管理员」。名称使用二进制精确匹配，自定义名称及其他账号保持原样。新建默认账号直接使用「系统管理员」。降级仅回退迁移版本，保留显示名，避免覆盖后来设置的名称；旧应用可继续读取。

## 2. organization（组织）

```sql
CREATE TABLE organization (
  id          CHAR(32)     NOT NULL PRIMARY KEY,
  name        VARCHAR(128) NOT NULL,
  is_platform TINYINT(1)   NOT NULL DEFAULT 0,          -- 1=内置「系统管理」组织，全局唯一
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

## 4. org_member（组织成员）

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

## 5. model_provider（供应商，系统级）

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

## 6. model（上架模型，系统级）

```sql
CREATE TABLE model (
  id             CHAR(32)     NOT NULL PRIMARY KEY,
  provider_id    CHAR(32)     NOT NULL,
  model_code     VARCHAR(128) NOT NULL,                 -- 调 API 的模型名：deepseek-chat / doubao-pro-32k / qwen-max
  display_name   VARCHAR(128) NOT NULL,
  capabilities   JSON         NOT NULL,                 -- {"tools":true,"reasoning":true,"vision":false,"streaming":true,
                                                        --  "reasoning_efforts":["low","medium","high"],"reasoning_effort_default":"medium"}（后两个可选；档位从 none/minimal/low/medium/high/xhigh/max 多选；空 = 不支持选档）
  context_size   INT          NOT NULL DEFAULT 64000,   -- tokens
  default_params JSON         NULL,                     -- {"temperature":0.7,"max_tokens":4096,...}
  status         VARCHAR(16)  NOT NULL DEFAULT 'active',
  created_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at     DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_model_provider_code (provider_id, model_code),
  CONSTRAINT fk_model_provider FOREIGN KEY (provider_id) REFERENCES model_provider(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

## 7. platform_setting（系统设置 KV）

```sql
CREATE TABLE platform_setting (
  `key`      VARCHAR(64) NOT NULL PRIMARY KEY,
  value      JSON        NOT NULL,
  updated_by CHAR(32)    NOT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

已定义的 key（新系统配置一律新增 key，不改表结构）：

| key | value 结构 | 默认 |
|---|---|---|
| `sandbox.default_image` | `string` | `"python:3.12"`（首次部署后由管理员替换为预装工具镜像，见 runtime.md §7.3） |
| `sandbox.images` | `string[]`（可选镜像列表，Agent 编辑器下拉源） | `["python:3.12"]` |
| `sandbox.egress_policy` | `{defaultAction:"allow"|"deny", rules:[{action:"allow"|"deny", target:string}]}` | `{"defaultAction":"allow","rules":[]}` |
| `sandbox.resource_limits` | `{cpu:string, memory:string}` | `{"cpu":"1","memory":"1Gi"}` |
| `sandbox.idle_ttl_seconds` | `number`（空闲多久暂停沙箱，见 runtime.md §7.2） | `28800` |
| `sandbox.ttl_seconds` | `number`（沙箱硬 TTL，随活动续约） | `3600` |

## 8. agent / skill / agent_skill / agent_subagent（组织级）

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
  tools_enabled  JSON          NOT NULL,                -- {"exec":true,"read":true,"write":true,"edit":true,"glob":true,"grep":true,"read_media":true}
  sandbox_config JSON          NULL,                    -- {"image":"","resources":{"cpu":"2","memory":"2Gi"},"env":{"KEY":"value"}}；image/resources 为电脑模板；env 每轮读取，exec 时覆盖电脑变量
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

`agent_subagent` 业务约束（service 层校验，DB 不表达）：同组织；`sub_agent_id != agent_id`；保存时做环检测（限 1 层深度：子 Agent 自身不得再出现在任何 agent_subagent 的 agent_id 列——即子 Agent 不能再有下级）；仅 published Agent 可被委派（被引用者下线时，主 Agent 的 task 工具描述中将其移除并记审计）。

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
  approval_mode  VARCHAR(16)  NOT NULL DEFAULT 'auto',    -- strict / default / auto
  model_id       CHAR(32)     NOT NULL,                 -- 会话级模型（创建时=agent.model_id，可切换）
  model_params   JSON         NULL,                     -- 会话级模型参数（如 {"reasoning_effort":"high"}），覆盖 agent.model_params；思考强度在 last_seq>0 后锁定
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
6. 会话必须绑定 `session.computer_id`；实例状态以 `computer` 为准，不能根据会话删除状态回收共享电脑。旧沙箱字段和旧会话数据由 000008 清理。

## 10. attachment（图片附件，组织级）

```sql
CREATE TABLE attachment (
  id         CHAR(71)     NOT NULL PRIMARY KEY,            -- "sha256:" + 64 位小写 hex（内容寻址，全局去重）
  org_id     CHAR(32)     NOT NULL,
  mime       VARCHAR(64)  NOT NULL,                        -- image/png / image/jpeg / image/webp / image/gif（上传时按魔数嗅探）
  bytes      BIGINT       NOT NULL,
  width      INT          NOT NULL,
  height     INT          NOT NULL,
  name       VARCHAR(256) NOT NULL DEFAULT '',
  created_by CHAR(32)     NOT NULL,
  created_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_attachment_org (org_id, created_at),
  CONSTRAINT fk_attachment_org FOREIGN KEY (org_id) REFERENCES organization(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

- 字节本体在库外内容寻址存储（§13），表只存元数据；org_id 记录首发组织，当前授权与租户文件名在 attachment_access 中（§10.1），宽高、MIME、大小为全局不变校验依据。
- 同字节内容全局只有一行；租户权限由 attachment_access 独立保存，不能因为首发组织不同而禁止其他组织上传相同字节。
- 写入方有两类：用户上传（`POST /sessions/{id}/attachments`）与 read_media 工具（agent 读沙箱图片入库，created_by 记会话所属用户 id；与用户上传同一套魔数/8MB/64M 像素闸门）。
- **附件永不自动删除**：删除会话不删 blob 与表行（fork 共享同一不可变对象）；引用感知 GC（扫描 `session_event` 引用 + orphan 清理）后续另做。

### 10.1 附件授权与供应商缓存（迁移 000006）

```sql
CREATE TABLE attachment_access (
  org_id CHAR(32) NOT NULL,
  attachment_id CHAR(71) NOT NULL,
  name VARCHAR(256) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (org_id, attachment_id),
  CONSTRAINT fk_attachment_access_org FOREIGN KEY (org_id) REFERENCES organization(id),
  CONSTRAINT fk_attachment_access_blob FOREIGN KEY (attachment_id) REFERENCES attachment(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO attachment_access (org_id, attachment_id, name, created_at)
SELECT org_id, id, name, created_at FROM attachment;

CREATE TABLE provider_media_file (
  scope CHAR(64) NOT NULL,
  attachment_id CHAR(71) NOT NULL,
  file_id VARCHAR(256) NOT NULL,
  expires_at BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (scope, attachment_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

scope 为租户、供应商身份、协议端点、凭据及请求版本的 SHA-256，不保存明文凭据。只缓存已经可用的文件；处理中的状态不持久化，失败不写映射。expires_at 为 Unix 秒，0 表示供应商未返回期限；缓存丢失可从本地附件重建。参见 [media.md](media.md)。

## 11. usage_daily / audit_log

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
  org_id     CHAR(32)    NOT NULL DEFAULT '',           -- 系统级操作为空串
  actor_id   CHAR(32)    NOT NULL,
  action     VARCHAR(64) NOT NULL,                      -- provider.create / provider.update_key / agent.publish / member.add / skill.upload ...
  target     VARCHAR(128) NOT NULL DEFAULT '',
  detail     JSON        NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_audit_org_time (org_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

`usage_daily` 聚合方式：runtime 在每个 `assistant/message` 事件提交后，从事件 `data.usage` 异步 `INSERT ... ON DUPLICATE KEY UPDATE ... = ... + VALUES(...)`（按事件发生日的本地日期）。子 Agent 的用量记在主 Agent 行上（事件中保留 `sub_agent_id` 供明细分析，报表 v1 不展示）。

## 12. 种子数据（初始种子与后续迁移完成后的状态）

```sql
-- 「系统管理」组织（固定 id 便于引用：全 0 变体不符合 UUIDv7，仅作内置常量，见 internal/id/wellknown.go）
INSERT INTO organization (id, name, is_platform) VALUES
  ('00000000000000000000000000000001', '系统管理', 1);

-- 默认系统设置
INSERT INTO platform_setting (`key`, value, updated_by) VALUES
  ('sandbox.default_image',    '"python:3.12"',                'system'),
  ('sandbox.images',           '["python:3.12"]',              'system'),
  ('sandbox.egress_policy',    '{"defaultAction":"allow","rules":[]}', 'system'),
  ('sandbox.resource_limits',  '{"cpu":"1","memory":"1Gi"}',   'system'),
  ('sandbox.idle_ttl_seconds', '28800',                        'system'),
  ('sandbox.ttl_seconds',      '3600',                         'system');
```

后续迁移新增的种子数据：

```sql
-- 000004_sandbox_env.up.sql：系统级沙箱默认环境变量（agent.sandbox_config.env 逐键覆盖）
INSERT INTO platform_setting (`key`, value, updated_by) VALUES
  ('sandbox.env', '{}', 'system');
```

## 13. 库外文件存储

| 内容 | 位置 | 说明 |
|---|---|---|
| Skill zip 解压目录 | `<data_dir>/skills/<org_id>/<skill_id>/` | 原始 zip 保留为 `_source.zip` 便于重新校验；路径配置化，保留对象存储扩展口 |
| 图片附件 blob | `<data_dir>/attachments/v1/objects/<sha256[:2]>/<sha256>` | 内容寻址、不可变、字节去重；写：临时文件→校验→原子 rename；读：按记录 sha256 校验后再服务（`internal/attachment`）。永不自动删除 |
| 云电脑持久文件 | 用户三类卷 `/storage`、`/agents`、`/sessions` | 生命周期独立于实例；容器运行层随销毁丢失 |

## 14. 云电脑与持久卷（000005_computers）

以下 DDL 是在上述基础表上的增量定义，与 `000005_computers.up.sql` 保持一致。资源归属由应用层同时校验组织成员关系和用户身份；外部资源操作不放在 SQL 事务中。

```sql
-- Additive migration. No backend resources are created or deleted by SQL.
CREATE TABLE computer (
  id CHAR(32) NOT NULL PRIMARY KEY,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  name VARCHAR(128) NOT NULL,
  backend_id VARCHAR(64) NOT NULL,
  sandbox_id VARCHAR(128) NULL,
  generation BIGINT UNSIGNED NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'stopped',
  config JSON NOT NULL,
  last_active_at DATETIME(3) NULL,
  last_error TEXT NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_computer_owner (org_id, user_id, status),
  KEY idx_computer_sandbox (sandbox_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_volume (
  id CHAR(32) NOT NULL PRIMARY KEY,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  kind VARCHAR(16) NOT NULL,
  backend_id VARCHAR(64) NOT NULL,
  backend_name VARCHAR(63) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  policy JSON NOT NULL,
  `usage` JSON NOT NULL,
  last_error TEXT NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_user_volume_owner (org_id, user_id, kind),
  UNIQUE KEY uk_user_volume_backend (backend_id, backend_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_agent_binding (
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  agent_id CHAR(32) NOT NULL,
  computer_id CHAR(32) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (org_id, user_id, agent_id),
  KEY idx_user_agent_binding_computer (computer_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_skill_state (
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  skill_id CHAR(32) NOT NULL,
  desired_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  applied_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  lock_owner VARCHAR(128) NOT NULL DEFAULT '',
  lock_until DATETIME(3) NULL,
  fence BIGINT UNSIGNED NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (org_id, user_id, skill_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE computer_run (
  id CHAR(32) NOT NULL PRIMARY KEY,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  computer_id CHAR(32) NOT NULL,
  session_id CHAR(32) NOT NULL,
  parent_run_id CHAR(32) NULL,
  agent_id CHAR(32) NOT NULL,
  status VARCHAR(16) NOT NULL,
  owner VARCHAR(128) NOT NULL DEFAULT '',
  lease_until DATETIME(3) NULL,
  execution JSON NOT NULL,
  manifest JSON NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_computer_run_computer (computer_id, status),
  KEY idx_computer_run_session (session_id, status),
  KEY idx_computer_run_parent (parent_run_id),
  KEY idx_computer_run_lease (status, lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE run_skill_lease (
  run_id CHAR(32) NOT NULL,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  skill_id CHAR(32) NOT NULL,
  revision BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (run_id, skill_id),
  KEY idx_run_skill_lease_skill (org_id, user_id, skill_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE resource_operation (
  id CHAR(32) NOT NULL PRIMARY KEY,
  org_id CHAR(32) NOT NULL,
  user_id CHAR(32) NOT NULL,
  resource_type VARCHAR(32) NOT NULL,
  resource_id CHAR(32) NOT NULL,
  action VARCHAR(64) NOT NULL,
  status VARCHAR(16) NOT NULL,
  idempotency_key VARCHAR(128) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL,
  request_hash CHAR(64) NOT NULL DEFAULT '',
  progress JSON NOT NULL,
  error TEXT NOT NULL,
  lease_until DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_resource_operation_idempotency (org_id, user_id, action, idempotency_key),
  KEY idx_resource_operation_lease (status, lease_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE session
  ADD COLUMN computer_id CHAR(32) NOT NULL,
  ADD COLUMN workspace JSON NULL,
  ADD KEY idx_session_computer (computer_id);
ALTER TABLE skill
  ADD COLUMN revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
  ADD COLUMN content_hash CHAR(64) NOT NULL DEFAULT '';

```

- `backend_id` 是配置中的稳定存储域身份，不等同于后端类型；`backend_name` 首次落库后不再渲染。更换端点不能作为迁移已存在卷的手段。
- `platform_setting` 中的 `volume.installation_id` 在首次卷操作时幂等建立，值为 JSON 字符串（32 位小写 UUID）。所有服务副本共享此身份；备份数据库时必须保留它。回滚 schema 不删除该标识或后端卷。
- 卷服务按 `(org_id,user_id)` 使用 MySQL 命名锁串行化创建与检查，锁使用专用连接；外部请求期间没有 SQL 事务。取消/释放失败时丢弃物理连接，避免持锁连接回到池中。意图先落库，后端 Ensure 成功后才写 ready；网络超时或 DB 写失败可用同一卷名重试。
- `ready/missing` 只允许 Inspect，不允许 Ensure；发现缺失写 missing。显式恢复的卷只有全部归属标签匹配时才能回到 ready。
- `config/policy/usage/execution/manifest/progress` 由模型写入前初始化为 `{}`，避免 JSON `NULL`；旧 `session.workspace` 保持 nullable 以兼容旧插入，新流程显式写入工作区状态。
- `skill.revision` 初始为 1；旧包 `content_hash` 初始空串，技能同步负责计算摘要。单版本安装不意味着丢失运行记录中的修订审计。
- `resource_operation.idempotency_key` 可空（无幂等键），非空值使用二进制排序规则；唯一约束按组织、用户、action 限定。相同键且 request_hash 不同由服务返回 conflict。
- `computer_run` 的活动主任务互斥由运行服务锁定 session 行后检查并登记，不能仅依赖进程 map。过期 lease 不等同于远端进程结束。
- 删除电脑使用 status=deleted，不级联删除用户卷。schema down 会删除资源索引表，执行前应导出资源身份；已有工作区新写入需要独立数据回滚计划。

## 15. 旧会话沙箱退役（000008）

历史 000001–000007 迁移保持原样。000008 清理未绑定电脑、持有旧实例 ID 或含旧 session/sandbox 事件的会话，以及关联事件、检查点、computer_run 和 run_skill_lease；删除 workspace/migrate 操作和维护运行。保留正常电脑会话及完整连续的事件序列，不改写其历史。删除 session.sandbox，computer_id 改为 NOT NULL。三卷、电脑、用户、Agent、Skill 和共享附件不删除。停止应用与远端执行后升级；down 仅恢复 schema，不恢复旧数据。


## 16. 网络搜索（000009）

搜索供应商实例与模型供应商独立；同类型没有唯一约束。`search_config` 固定 id=1，默认初始为空；`agent.search_provider_id` 为可空外键（null 继承当前全局默认）。调用账本与模型 token 分离，详细行为见 [search.md](search.md)。以下为增量 DDL，应用于上述既有表结构：

```sql
CREATE TABLE search_provider (
  id CHAR(32) NOT NULL PRIMARY KEY,
  name VARCHAR(128) NOT NULL,
  type VARCHAR(32) NOT NULL,
  base_url VARCHAR(512) NOT NULL,
  api_key_enc BLOB NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  created_by CHAR(32) NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE search_config (
  id TINYINT NOT NULL PRIMARY KEY,
  default_provider_id CHAR(32) NULL,
  CONSTRAINT fk_search_default FOREIGN KEY (default_provider_id) REFERENCES search_provider(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
INSERT INTO search_config (id, default_provider_id) VALUES (1, NULL);

ALTER TABLE agent ADD COLUMN search_provider_id CHAR(32) NULL AFTER tools_enabled,
  ADD CONSTRAINT fk_agent_search_provider FOREIGN KEY (search_provider_id) REFERENCES search_provider(id);

CREATE TABLE search_call (
  id CHAR(32) NOT NULL PRIMARY KEY,
  provider_id CHAR(32) NOT NULL,
  org_id CHAR(32) NOT NULL DEFAULT '',
  user_id CHAR(32) NOT NULL DEFAULT '',
  agent_id CHAR(32) NOT NULL DEFAULT '',
  session_id CHAR(32) NOT NULL DEFAULT '',
  call_id VARCHAR(128) NOT NULL DEFAULT '',
  success BOOLEAN NOT NULL,
  result_count INT NOT NULL DEFAULT 0,
  duration_ms BIGINT NOT NULL,
  credits DOUBLE NULL,
  error_code VARCHAR(32) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_search_call_provider (provider_id, created_at),
  KEY idx_search_call_org (org_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```
