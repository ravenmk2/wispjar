# 用户云电脑、持久卷与并行执行

> 状态：2026-10-20 已批准设计，分阶段实现；交付状态见 §12。
> 本文是 `internal/computer`、`internal/volume` 及其与 sandbox/runtime/session/skill 的集成契约。
> 涉及云电脑资源归属、目录、技能同步的规则以本文为准；旧会话沙箱只作为迁移兼容路径。
> 相关文档：[系统总览](../system-design.md)、[数据模型](data-model.md)、[API](api.md)、[运行时](runtime.md)、[实时协议](protocol.md)、[前端](frontend.md)。

## 1. 目标与不变量

1. 一个用户在一个机构内拥有多台逻辑云电脑；`computer_id` 稳定，底层 `sandbox_id` 可重建，`generation` 单调递增。
2. 每个 `(org_id,user_id)` 只有 storage/agents/sessions 三个逻辑持久卷，所有电脑挂载同一组卷。
3. 同一电脑允许多个会话并行；同一会话维持一个主 turn，主任务可并行委派 subagent。
4. 会话固定绑定电脑，空闲时可显式切换；subagent 继承主任务电脑，不解析自己的电脑偏好。
5. 删除会话、删除电脑、沙箱租约到期均不隐式删除用户卷。
6. 数据保留优先级 `storage > agents > sessions`；没有明确配置的保留期时，不自动清理持久数据。
7. 同一用户对同一 skill 只开放一个安装修订，技能更新在任务边界进行，不提供多版本选择。
8. 同一用户的挂载文件构成共享信任域。会话目录是组织边界，不宣称是文件访问的强隔离边界。
9. 资源查询和操作同时校验机构成员关系及用户归属；机构管理员不因管理员身份获得用户私人电脑和文件。
10. 未确认远端进程停止之前，不因数据库租约过期就释放技能使用权或开始覆盖安装。
11. 新电脑的 execd 命令端点必须拒绝匿名调用，包括任务进程从实例内部访问的请求。电脑配置的 Env 仅传给非 root 任务进程，不作为容器或平台维护进程环境。缺少执行鉴权或任务环境隔离标记的旧实例返回 `computer/rebuild_required`，经显式重建保留三卷数据后继续使用。

## 2. 目录契约

```text
/storage/                              用户长期资产，自由组织
/agents/<agent_id>/
  files/                               当前用户的 Agent 跨会话文件
  skills/<skill_name>/                  当前生效技能，无版本子目录
    SKILL.md
    scripts/
    assets/
/sessions/<session_id>/                 会话工作空间，默认 cwd
  inputs/                              创建工作区时自动创建
  outputs/                             创建工作区时自动创建
```

- `inputs` 放上传材料或选定文件的副本，默认保持原始输入；需要修改时另存。
- `outputs` 是成果整理约定；发送到聊天的附件仍保存独立内容版本，不直接引用可变工作文件。
- 会话根目录可直接容纳代码仓库、分析脚本、项目结构，不再嵌套 `workspace/`。
- 并行子任务可按需使用会话下 `.tasks/<run_id>/`；共同修改项目时，主任务明确文件分工。不得自动清理可能仍被引用的子任务成果。
- 每次 exec 显式设置 cwd、任务环境；相对路径以会话 cwd 为根解析。绝不能通过全局 `/workspace` 软链接切换会话。
- `/tmp`、系统目录、HOME 与镜像运行层不承诺重建后保留。镜像和可重建配置负责长期环境。
- 平台的 `storage.data_dir` 保存附件和技能源包，与云电脑挂载点 `/storage` 不同。
- 跨卷“保存到我的存储”采用复制、校验、完成提交，不依赖跨卷 rename；默认不删除源文件。

## 3. 模块边界

| 模块 | 拥有的责任 | 不应承担 |
|---|---|---|
| `volume` | 卷命名、后端 provisioning/inspection、归属标签、三卷解析 | 会话运行、模型调用、HTTP 鉴权策略 |
| `computer` | 用户电脑、绑定、生命周期协调、资源操作、运行登记 | 工作区文件操作、SDK 数据面细节、LLM 事件翻译 |
| `sandbox` | OpenSandbox 实例、句柄、命令/文件、底层生命周期 | 用户偏好、删除会话、用户卷回收决策 |
| `skill` | 源包校验/替换、revision/hash、用户安装协调 | 创建电脑、Agent 主循环 |
| `runtime` | 执行上下文、工作区初始化/文件操作、主/子任务、技能使用权、取消、模型事件 | 卷命名、直接调用 Docker |
| `session` | 会话归属/电脑引用、事件、分叉、保留状态 | 沙箱实例的所有权 |
| `gateway` | DTO、鉴权上下文、路由接线、协议传输 | 资源业务流程 |

依赖由入口组装。消费者声明窄接口，后端适配器实现接口；不得将 `computer`、`runtime`、`skill` 互相循环引用。领域返回类型化错误，HTTP 层统一转为已有错误信封。避免为一次实现建立通用插件框架。

第一版应用运行时仍为单服务实例：会话调度、审批和事件总线保存在该实例内存中。数据库锁保护资源操作和持久状态，不代表已经支持多副本运行同一会话；横向扩容需另行实现运行归属、消息路由和故障接管。

卷 provider 最少支持 Ensure/Inspect；删除能力独立且只有显式资源清理流程可调用。沙箱适配层接收已经解析的挂载计划，不能自行推导卷归属。

## 4. 配置、卷命名与后端

扩展现有 `config.yaml`，沿用 `-config` 参数：

```yaml
storage:
  data_dir: "./data"
  volumes:
    backend: "docker"
    backend_id: "local"
    namespace: "main"
    name_pattern: "wj-{namespace}-{kind}-{volume_id}"
    docker_endpoint: "unix:///var/run/docker.sock"
runtime:
  max_parallel_tasks: 4 # 每个主 turn 的 subagent 并行上限，1..32
```

端点仅由管理员部署配置提供，不向云电脑或用户 API 暴露 Docker socket。未配置/不可达时明确返回资源不可用，不回退成非持久实例。

变量：`namespace`、`kind`、`volume_id`、可选 `org_id`、`user_id`；模板必须包含 `volume_id`。只做字面替换，不执行模板代码。渲染后匹配 `[a-z0-9]([a-z0-9-]*[a-z0-9])?`，最长 63 字符，未知变量/超长/非法字符均拒绝，不截断。namespace 默认 main，建议不超过 8 字符。

卷名首次创建后保存在 `user_volume.backend_name`；更改 pattern/namespace 只影响新卷。不要在每次挂载时重新计算卷名。

后端资源标签包含 managed、installation、volume-id、org-id、user-id、kind。部署标识持久保存，不能每次启动随机变化。同名资源只在标签全部匹配时幂等复用；禁止接管他人卷。已有 ready 卷丢失时标记 missing，不新建同名空卷。

第一版 Docker provider：通过配置端点创建/检查命名卷；OpenSandbox `Volume{PVC.ClaimName, MountPath}` 挂载已存在卷，名称分别为 storage/agents/sessions。卷生命周期不绑定沙箱终止。SDK 已有对应字段，部署端兼容性仍需真实集成测试。

新模式的镜像需提供 Linux、Python 3、可切换到 UID/GID 65532 的平台维护进程，以及 `prctl` 子进程回收能力。仓库的 `scripts/sandbox-image/Dockerfile` 满足运行依赖；任意用户镜像不能仅凭名称加入可用列表，应先通过执行、安装和文件操作验收。任务进程以 UID/GID 65532 运行，平台维护权限不向任务开放。

Docker 模式使用 execd 自身的 `EXECD_ACCESS_TOKEN` / `X-EXECD-ACCESS-TOKEN` 鉴权；OpenSandbox 的 `secureAccess` 为另一层入口鉴权，当前 Docker runtime 不支持，不能用它代替实例内部的访问保护。适配器以 lifecycle API key 为密钥，按 installation、computer ID、generation 通过带域分离的 HMAC-SHA256 派生执行令牌，仅注入平台 daemon；不写入 API、metadata 或电脑配置。服务重启可重建同一令牌，空密钥拒绝创建；更换 lifecycle 密钥后须显式重建已有电脑。SDK 代理模式的默认 header 回退在限定的 execd 代理路由上覆写为该令牌。任务环境不继承 daemon 令牌，非 root 任务不得读取 root 进程环境。

一个用户的三卷与其电脑必须位于同一个 backend/storage domain。`backend_id` 是该存储域的稳定身份，切换到其他后端时不得重新解释已有卷名。第一版不支持普通本地卷跨宿主机调度。未来共享存储 provider 可扩展，不改变数据库身份或容器路径。配额能力由 provider 声明，不把用量估算称为硬配额。

## 5. 数据模型

遵守 data-model.md：单数表名、32 位 ID、DATETIME(3)、软删除、配置 JSON。DDL 与模型由任务 T1 落地并同步数据模型文档。

| 表 | 字段（除通用 created_at/updated_at） | 约束 |
|---|---|---|
| computer | id, org_id, user_id, name, backend_id, sandbox_id(nullable), generation, status, config(JSON), last_active_at, last_error | index(org_id,user_id,status), index(sandbox_id) |
| user_volume | id, org_id, user_id, kind, backend_id, backend_name, status, policy(JSON), usage(JSON), last_error | unique(org_id,user_id,kind), unique(backend_id,backend_name) |
| user_agent_binding | org_id, user_id, agent_id, computer_id | primary(org_id,user_id,agent_id), index(computer_id) |
| user_skill_state | org_id, user_id, skill_id, desired_revision, applied_revision, status, lock_owner, lock_until, fence, last_error | primary(org_id,user_id,skill_id) |
| computer_run | id, org_id, user_id, computer_id, session_id, parent_run_id(nullable), agent_id, status, owner, lease_until, execution(JSON), manifest(JSON) | index(computer_id,status), index(session_id,status), index(parent_run_id), index(status,lease_until) |
| run_skill_lease | run_id, org_id, user_id, skill_id, revision | primary(run_id,skill_id), index(org_id,user_id,skill_id) |
| resource_operation | id, org_id, user_id, resource_type, resource_id, action, status, idempotency_key(nullable), request_hash, progress(JSON), error, lease_until | scoped unique(org_id,user_id,action,idempotency_key), index(status,lease_until) |

- `session` 增加 nullable `computer_id`、`workspace` JSON、computer_id 索引。workspace 有 state/layout_version/retention_until；路径由 ID 计算。
- `skill` 增加 revision（从 1 开始）、content_hash（内容清单摘要）；保持 skill.id 和 name 稳定。
- `agent.sandbox_config` 过渡期保留，作为新建电脑/迁移的环境模板，不覆盖已有电脑环境。
- `session.sandbox` 只用于旧数据兼容，computer 表是新实例状态权威来源。
- `manifest` 保存实际使用的 Agent/skill 修订与摘要、电脑 generation，用于审计，不创建历史安装版本。
- 同一会话至多一个活动主任务，必须通过事务/条件写入保证，不能只靠进程 map。
- 资源创建遵循 DB 意图→外部 Ensure→DB ready 的幂等状态机，不在 SQL 事务内持有长时间网络调用。重启恢复未完成操作。
- resource_operation 的幂等键相同但请求摘要不同返回 409。

## 6. 电脑与会话生命周期

电脑：stopped → starting → running → pausing → paused；重建经 rebuilding，删除经 deleting，终态 deleted，故障 failed。显式终止恢复经 recovering → stopped。generation 参与更新条件，迟到的旧实例事件不得覆盖新实例。

卷：pending → provisioning → ready；缺失 missing，失败 error。默认不提供普通电脑删除流程中的卷级删除。

绑定优先级：创建会话显式 computer_id → user_agent_binding → 返回 computer/required，要求选择或创建；不得随机选电脑。会话创建后固定引用，只有无活动任务时可切换。

电脑镜像、资源在创建时确定：显式电脑配置 → 选定 Agent 模板 → 平台默认，修改需空闲后重建。Agent 模板不复制 env；电脑 env 在创建时由平台默认与显式配置按键合并，之后可直接编辑，无需重建。每轮任务固定电脑 env 和主/子 Agent 最新 env，exec 按电脑 → 当前 Agent → 单次命令依次覆盖。子 Agent 使用自己的变量，不继承父 Agent 变量；镜像和电脑仍跟随主任务。变量仅注入非 root 任务，不影响 execd 或维护进程。

暂停：无活动 run、无后台执行/维护使用权、超过 idle TTL。SDK 租约与数据保留期分开。删除会话只取消其任务；删除电脑遇绑定/运行返回 conflict，要求先迁移。实例丢失可恢复电脑，但中断中的命令不得自动重放（可能产生重复副作用）。

服务重启按 installation/computer_id/generation 标签接管，禁止用“session 已删除”推断共享电脑可以删除。旧会话标签单独走兼容迁移策略。新电脑 metadata 的 `wispjar` marker 使用独立代际/installation 值，不复用旧管理器扫描的 `wispjar=1`，避免新旧应用并存或测试实例共用 OpenSandbox 时被旧 reaper 当成孤儿删除。

实际 marker 为 `computer-<installation>`（满足后端标签字符约束）。`opensandbox.legacy_sessions_enabled` 默认为 true 保留旧部署兼容；新独立部署共用已有 OpenSandbox 时必须设为 false，完全关闭旧 session 管理器的创建与扫描。仅更换电脑 marker 不能阻止旧管理器扫描其他部署的旧 session 实例。

运行记录租约失效后先核查命令，再回收使用权；不能把数据库心跳失效当作远端进程已死。无法确认时保持阻塞并暴露待处理状态。

用户可明确选择“终止任务并恢复”：`POST /computers/:id/recover` 必需 `confirm_stop_all:true`，表示允许终止该电脑全部正常或遗留任务。Intent 在电脑行锁内进入 recovering，保留旧实例身份并拒绝新运行；worker 持电脑操作锁，逐个删除该意图涉及的旧实例并查询确认 missing。确认前保留所有占用；超时、身份不符或尚未完成的异步删除均保持屏障及可见重试状态。确认后事务将相关运行终结并释放根运行技能租约，记录审计；再协调技能同步恢复和旧会话运行退役，全部成功才增加 generation、清除实例 ID 并进入 stopped。中途重启继续同一持久操作，已终结运行的迟到 FinishRun 不得覆盖其终态。恢复保留三卷、会话绑定和默认电脑选择，下一次启动创建新实例。

## 7. 运行上下文与技能

每个主/子任务有独立 Context：org/user/computer/generation/session/run/parent_run/agent、cwd、agent_files_dir、skills_dir、技能清单、工具 meta、UsageSink、取消范围。共享底层电脑句柄，不共享这些可变字段。

同一电脑多会话并行；同一会话 subagent 并行，数量受配置上限约束。用量按模型请求/子任务归属采集，不能复用串行时代的全局 TakeLatest。取消只中断对应命令及其子进程；后台进程登记并持续阻止不安全暂停。

技能：

1. 技能源包只公开当前 revision；包替换校验并保持稳定 skill ID/name。
2. 同一用户、同一技能的所有 Agent 安装副本保持同一个生效 revision。
3. 主 run 启动前解析当前主/子 Agent 技能依赖，按稳定顺序原子取得整组使用权；不得先持有部分旧技能再等待另一部分更新。
4. 有更新时设屏障，阻止相关新主任务；已有任务包括审批等待可完成，不自动取消。
5. 现有使用者全部结束且远端无相关命令后，同步所有相关安装目录。清除新版删除文件，校验清单，整体完成后开放任务。
6. 暂存包不对 Agent 开放为第二个版本；部分失败保持 blocked/error，恢复完成前不可执行混合内容。
7. skill 工具从该 run 使用的已安装内容读取说明，不能读已更新的源包搭配尚未更新的脚本。
8. 提示词、模型、编排仍使用会话快照；技能绑定/内容和 Agent env 在新主任务边界解析最新状态，是明确的快照例外。主/子 Agent env 一次查询、本轮固定；读取失败则准备失败，不回退旧凭据。技能记录 revision/hash 与必要的环境变更事件，env 值不写入新增事件，保持历史事件不变。
9. 修改技能关联也走同样的目录协调，不删除活跃任务还在用的技能。
10. 技能目录平台维护、任务只读；files 目录任务可写。需要非 root 任务身份、目录权限及文件工具写入策略共同落实。

T3 实现约定：

- `internal/skill.Coordinator` 通过注入的 Installer 工作，不依赖 computer/runtime。`Acquire` 要求已登记 root run，依赖集合含全部可达子 Agent；同 root run 重试返回原 manifest，释放后不得重新获取。整组租约与 `computer_run.manifest.skill_manifest` 同事务落库。
- 使用按机构/用户的跨进程 advisory lock 串行化安装协调；实际发布还在共享卷上持有 flock，并验证单调 fence。`syncing/blocked` 不因时间过去自动恢复；只有确认远端停止后的 `Recover` 才可推进 fence 并重试，动作记录审计。
- 同步操作的持久 `InstallPlan` 包含从 `computer_run` 读取的 `root_run_id/computer_id/generation`，不接受调用方自行声明归属。电脑恢复先删除旧实例并 Inspect 确认消失、持久终结旧代次运行，再调用 `Coordinator.RecoverComputer(ctx, orgID, userID, computerID, oldGeneration)`。该 hook 在用户技能锁下清理对应根运行使用权，仅将归属和 fence 均匹配的未知同步转为 pending 并推进 fence；不影响其他电脑、其他代次或更晚同步。旧无归属计划继续 blocked，须另行确认该用户相关同步进程全部停止后使用审计恢复，不能根据时间推断。
- 锁顺序为电脑 advisory lock → 用户技能 advisory lock → run 行锁 → 技能状态行锁；调用恢复 hook 时不能持有 SQL 事务或 run 行锁。技能协调不反向获取电脑锁。安装意图、安装结果和最终整组取权事务都重新锁定运行行，检查终态、使用权释放标记和电脑代次；晚到结果不能重新插入租约或覆盖恢复后的 fence。
- Linux Python3 安装器使用 root-only `/agents/.wispjar/inbox` 和暂存目录；拒绝被替换成符号链接或非 root 所有的目录。先验证摘要并准备完整内容，再替换所有副本、移除旧文件；部分失败保持屏障。任务不得取得 root 安装能力。
- 每次新任务同时对比当前关联与上次安装目标，解除关联时在安全边界清理旧副本。删除源包拒绝任何活跃租约、未确认同步或未退役的安装副本。
- 源包采用隐藏候选目录与单一数据库 StoragePath 指针提交；提交结果未知时保留候选。`Service.RecoverSources/RecoverAllSources` 在源锁下读取当前指针后清理非当前候选，包含首传中断留下的孤立候选。API 只暴露当前包。
- 验收：本地技能单测通过；真实 MySQL 并发取权、修订竞争、审批恢复、屏障、故障重试与退役删除通过；隔离 Linux 容器内真实 Python 安装、旧文件删除、只读权限、stale fence、符号链接拒绝通过。运行时接线由 T4 完成。

## 8. REST 契约

前缀 `/api/v1`，沿用现有错误信封和机构认证。资源查询均校验本人归属。字段详细定义由 API 文档同步维护。

| 方法/路径 | 契约 |
|---|---|
| GET/POST /orgs/:orgId/computers | 列表 / 创建；创建 body name、config、可选 agent_id 模板 |
| GET/PATCH/DELETE /computers/:id | 详情 / 仅修改名称 / 删除；环境修改通过 rebuild config；有运行或绑定时拒绝删除 |
| POST /computers/:id/start | stopped/paused 启动；running 幂等成功 |
| POST /computers/:id/pause | 有活动执行返回 computer/busy |
| POST /computers/:id/rebuild | 空闲重建，generation 递增，保留三卷 |
| GET /computers/:id/runs | 活动任务与状态 |
| POST /computers/:id/recover | 显式终止全部任务并恢复，需 confirm_stop_all:true；保留三卷，完成后 stopped |
| GET /orgs/:orgId/agent-bindings | 当前用户的绑定 |
| PUT /orgs/:orgId/agent-bindings/:agentId | body computer_id；验证同一机构和用户 |
| DELETE /orgs/:orgId/agent-bindings/:agentId | 幂等清除个人默认电脑偏好，不修改已有会话引用 |
| GET /orgs/:orgId/volumes | 三卷摘要，不回显后端凭据或宿主位置 |
| GET /operations/:id | 操作状态与资源摘要 |
| GET /sessions/:id/files | 相对 path，默认 "."；返回 root/path/items |
| GET /sessions/:id/file | 相对 path；沿用预览限制与下载行为 |
| POST /sessions/:id/files | multipart file + 相对目录 path，默认 "."；scope 显式选择当前会话、storage 或 agent 文件，文件名冲突不静默覆盖 |
| POST /sessions/:id/files/save-to-storage | source 相对会话路径，destination 相对 storage 路径；复制校验 |
| POST /sessions/:id/downloads | body path/scope/archive；202 返回后台下载任务 |
| GET /sessions/:id/downloads/:downloadId | 只读查询 preparing/ready/failed/canceled；ready 含浏览器下载 URL |
| DELETE /sessions/:id/downloads/:downloadId | 取消准备或撤销就绪任务并清理临时文件 |
| GET、HEAD /downloads/:downloadId/file | 256 位随机、限时、仅授予固定文件副本的下载凭证；再次核对账号、会话归属和机构成员关系，支持 Range/If-Range |
| PUT /orgs/:orgId/admin/skills/:id/package | multipart 包替换，预期 revision 防止覆盖并发更新 |

新增电脑/重建等耗时操作返回 202 `{operation_id,status,resource_id}`；创建与重建接受 Idempotency-Key。操作轮询只读，不隐式启动实例。

创建会话增加 computer_id；PATCH 仅空闲时可换电脑；会话详情增加 computer 摘要/workspace。旧 sandbox/file(s) 端点过渡期代理新服务。

文件端点默认限制在对应资源根，校验绝对/相对路径、`..`、符号链接及实际打开对象；不能把字符串前缀检查当成完整保护。通用文件上传与现有图片 attachment 准入分开。

文件浏览只枚举当前目录及直接子项的元数据，文件内容按预览请求读取。已有工作区不因浏览重复创建目录或修改权限；仅资源根缺失时补做初始化后重试，普通子项不存在仍返回 404。小参数通过 stdin 交给受监督的非 root 进程，单次列目录不创建或上传临时参数文件；大文件传输保留受保护暂存机制。鉴权、会话维护占用、远端停止确认与符号链接限制保持生效。

新下载通道与预览、上传分离：默认单文件、目录原始内容及生成 ZIP 各限 20 GiB，最多 100,000 个条目，目录深度最多 128；应用实例的本地下载暂存总配额 40 GiB。限制由 `storage.downloads` 配置，准备时限及就绪下载有效期各默认 30 分钟。全实例最多 4 个准备任务、同一用户最多 2 个，任务总数最多 128；资源不足明确失败。上传仍限 32 MiB。

准备任务注册会话维护占用；平台进程先创建 root 专属临时文件，再由降权到任务 UID/GID 的子进程通过继承的输出 FD 读取源目录，逐级 NOFOLLOW 打开路径。单文件复制和 ZIP64 打包均有界分块读写，不使用完整 base64/Blob。ZIP 包含顶层目录、空目录、隐藏文件及普通子文件；符号链接、特殊文件、不可读路径或打包期间的文件变化使整次打包失败，不静默漏文件。已经压缩的常见媒体与二进制资源采用 ZIP_STORED，其他文件采用 DEFLATE。

远端父进程监督子进程的超时与取消并等待其终止，确认停止后才释放维护占用；传输不明仍保留占用供恢复。生成的不可变远端文件经 SDK 流读取到应用 `data_dir/.downloads`，边传输边校验长度与计算摘要，随后删除远端副本。浏览器通过固定副本原生下载；HTTP ServeContent 提供 Range/If-Range/ETag 和 64 位文件长度。原有预览与兼容 `download=1` 端点不用于大文件。下载有效期内可重试／续传，过期或撤销清理本地副本；重启会使凭证失效，遗留目录在后续准备任务中按过期时间回收。远端崩溃遗留物在后续导出时按两小时回收。

命令数据流收到 `execution_complete` 后即可结束读取，不继续等待 HTTP 流关闭或下一次心跳。完成事件之前的标准输出、标准错误和退出状态必须完整保留；传输中断仍按未知完成处理，资源释放继续要求监督进程的停止标记。

错误码至少包含 computer/required、computer/not_found、computer/busy、computer/in_use、computer/unavailable、volume/conflict、volume/missing、skill/update_pending、skill/sync_failed、operation/conflict；400 参数错误、404 资源不可见、409 状态冲突、502/503 后端故障按现有规范映射。

## 9. 实时协议与前端

control 流在现有会话 baseline/upsert/remove 外增加 computers 基线与 computer/upsert、computer/remove；首帧包含完整当前可见电脑列表，重连覆盖旧投影。电脑状态不再从任意一个会话历史推断。

会话 snapshot header 增加 computerId、computerStatus、workspaceDir；持久事件增加 session/computer（绑定变化）、workspace/ready、runtime/environment（实际技能清单变化）；工具结果 meta 增加 computer_id/run_id，保留 sandbox_id 诊断。

前端提供：
- 侧栏左下角用户菜单的「云电脑」进入管理页：创建、启动、暂停、重建、删除、活动任务、操作进度。
- 侧栏智能体「… → 云电脑设置」提供个人绑定，管理页集中维护。创建会话自动采用绑定，不再在草稿页选择电脑；缺少绑定时保留输入并提示前往设置。
- 聊天主内容区不再展示独立云电脑整栏，已有会话继续使用原绑定；API 保留空闲时显式切换电脑的能力。
- 文件面板当前会话、我的存储、Agent 文件；目录选择、上传、刷新、关闭合并顶部，outputs 优先、保存到存储、通用输入上传。新旧文件面板共用可拖拽调宽和宽度记忆，窄屏覆盖显示；聊天／轨迹切换缩小为紧凑按钮。
- 电脑失败在管理页显示脱敏后的原因和处理建议，聊天任务错误继续按会话错误机制呈现；“排查信息”提供错误码、操作编号，异步失败通知使用相同原因。实例缺失且保留失败创建标记时提示重建，不误导用户反复启动；旧记录没有具体原因时明确提示无法还原。
- 技能更新等待/失败提示；显示阻塞原因，不悄悄终止用户任务。
- 沿用 theme.md tokens、既有布局与组件，不引入独立设计系统。

## 10. 迁移与回滚

1. 增量 schema，旧 sandbox 数据继续保留。旧会话兼容路径与新电脑路径显式分离。
2. 只读盘点生成迁移清单（含旧沙箱、镜像、工作区、其他用户文件），提供 dry-run。
3. 幂等建立三卷；按会话停止新任务并等待活动执行结束。
4. 导出旧工作区到 `/sessions/<sid>`，预检 inputs/outputs 同名普通文件冲突；保留目录权限、必要元数据、符号链接语义并校验内容。
5. 第一轮每个旧沙箱生成一个迁移电脑，保留镜像/config，不自动合并。
6. 新实例挂载并验证后，再提交 session.computer_id。旧数据保留到显式验收清理。
7. 原实例已经丢失时明确标记不可恢复，不把空目录报告成恢复成功。
8. 旧历史 `/workspace` 不批量改写；新环境说明给出新路径。不创建电脑级全局兼容软链接。
9. fork 默认沿用消息历史复制且新工作区为空；复制当前文件若后续提供，必须明确不是 at_seq 时刻快照。
10. schema down 不删除后端卷；新工作区产生写入后，回滚需要独立数据方案，不能只回滚 SQL。

迁移实现与线上执行分开；本次代码交付不自动操作任何实际部署或旧实例。

实现位于 `internal/workspacemigration`，`cmd/wispjar-migrate` 只解析参数和组装依赖。工具要求显式会话 ID 批次，不支持隐式全量迁移：

```sh
# 只读盘点；不执行 schema migration、不创建安装身份/卷/电脑、不恢复暂停实例
go run ./cmd/wispjar-migrate -config config.yaml -sessions <session-id>,<session-id>
# 已确认旧任务和先前迁移命令均停止后执行
go run ./cmd/wispjar-migrate -config config.yaml -sessions <session-id> -apply -confirm-stopped
```

盘点和执行均核对旧实例 ID 以及旧管理器的 `wispjar=1`、会话归属、创建者 metadata。迁移在会话行锁下检查 `running` 与非终态 `computer_run`，写入 `workspace.state=migrating` 后阻止任务启动、文件维护和电脑重新绑定；操作进度保存在 `resource_operation` 的 `workspace/migrate` 操作中。目标电脑持有维护使用权，只有新工作区校验完成、源实例和会话绑定未改变、且会话未删除并无活动执行时才 CAS 提交新绑定。

迁移范围仅为 `/workspace`。清单记录内容摘要、文件类型、rwx mode、mtime、符号链接目标和原 UID/GID，导出前后及目标导入后再次扫描源目录；任一变化返回 `source_changed`，不切换绑定。目标将所有者映射为任务 UID/GID `65532`，保留 rwx mode 和 mtime，去除特殊权限位。仅允许指向旧工作区内部的符号链接，内部绝对链接转换为等价相对链接；越界链接、设备/FIFO 等特殊文件及 inputs/outputs 同名非目录均拒绝。缺少的 inputs/outputs 自动创建。第一版单次上限为 100,000 个条目、1 GiB 文件内容；归档流还受独立大小限制。

归档经本地受限临时文件转移，远端暂存目录只允许 root 使用；目标先在 `/sessions/.wispjar/migrations/<operation-id>/workspace` 校验完整清单，再原子发布到 `/sessions/<sid>`。目标已存在时，仅接受与完整清单一致的内容，不覆盖有差异的目录。`/root`、`/home`、`/tmp`、手工安装的软件和进程环境不随工作区复制，盘点输出明确列出该限制；新电脑保留旧镜像和会话环境配置，但平台当前网络策略继续生效。

失败状态保留会话迁移门禁和持久操作，旧实例与源文件始终保留。未知远端结果不能凭超时自动重试；核实命令停止后，以相同参数和 `-confirm-stopped` 重试，工具重读操作、验证已发布目标并继续提交。同一已完成操作重复执行返回原结果。若源已变化或目标已产生其他写入，工具保持阻塞，须另行核对数据后制定恢复方案，不提供覆盖目标或自动回滚删除。迁移临时归档与目标暂存不会作为会话输入输出暴露，残留暂存由维护流程核验后处理。

## 11. 验收矩阵

- 配置非法模板拒绝；改配置不改变已登记卷名；同名异主卷拒绝接管。
- 同用户两电脑共享三卷；不同用户无法挂载对方卷。
- 实例删除/重建/过期后文件保留；ready 卷丢失不能自动创建空卷。
- 两会话同机同时 exec 各自 cwd；取消 A 不影响 B；删除 A 不回收电脑。
- subagent 电脑跟随、技能跟随自身 Agent，事件/审批/用量不串，支持并行。
- 技能更新等待活跃任务，新增与删除文件均同步，失败不开放混合版本，跨电脑协调。
- 服务重启恢复操作和电脑 generation；过期租约需核查实际命令。
- 文件上传/下载/保存、路径与符号链接边界、历史附件稳定。
- 迁移 dry-run 无写入，重复执行幂等，校验失败不切换，旧数据不提前删除。
- Go 单测与 race（环境支持时）、前端类型/构建与现有测试；真实 MySQL/Docker/OpenSandbox 集成验收独立记录，不用 mock 通过代替。

## 12. 实施任务与责任

根 Agent 负责设计、接口冻结、集成、审查与验收。子任务按依赖逐个派发，跨域修改先协调，不允许并发改同一契约。

| 任务 | 内容 | 依赖 | 状态 |
|---|---|---|---|
| T0 | 本设计及所有相关文档、索引同步 | 无 | 设计落地 |
| T1 | 配置/卷 provider、七表与迁移、领域 DTO 基础 | T0 | 基础验收通过：单测、MySQL 8 全迁移；真实挂载随 T2/T6 验收 |
| T2 | computer 服务/生命周期/操作 API、三卷挂载、电脑接管 | T1 | 已实现；真实挂载、持久化、MySQL 并发及恢复保护回归通过 |
| T3 | 技能包更新、单版本同步与运行使用权 | T1,T2 接口 | 已实现并接入运行时；真实 MySQL、Linux Python 安装、未知执行恢复和迟到取权回归通过 |
| T4 | 会话绑定/文件服务、运行上下文、并行 subagent 与取消 | T2,T3 | 已实现；单测/race、真实会话门禁及进程回收、文件路径边界回归通过 |
| T5 | 前端电脑管理/选择/文件面板、WS 契约接入 | T4 | 已实现；49 项前端测试、类型检查及构建通过，电脑/文件交互已用浏览器验证 |
| T6 | 旧数据盘点/迁移工具与恢复、跨模块集成验收 | T1–T5 | 迁移工具已实现；真实 MySQL 与隔离 Python 文件迁移通过，完整旧实例适配器验收仍待执行 |

每个任务提交真实修改范围、测试命令与结果、遗留限制，由根 Agent 审查后才标记验收通过。文档不得把未完成的能力写成已交付。

阶段验收记录（环境标识仅保存在忽略的操作目录）：
- 2026-10-20：改造前 Go 全量、runtime/tools/sandbox race、前端 40 项测试通过。
- T1：真实 MySQL 8 全迁移通过；迁移幂等及 GORM repository 并发/取消集成测试通过。验收发现的 usage 保留词与测试清理顺序问题已修复。
- T2 底层挂载：显式集成测试 `TestComputerPersistenceIntegration` 通过，两电脑共享三卷、错归属拒绝、暂停恢复与重建保留文件；逻辑电脑服务与 API 仍待独立验收。
- T2 数据库：`TestRepositoryConcurrencyIntegration` 在真实 MySQL 8 通过，覆盖创建幂等、并发主任务唯一性、多会话共机、子任务结束顺序、维护互斥、归属/代次校验与迟到结果拒绝。
- 共享锁：`TestAdvisoryLockDoesNotStarvePoolIntegration` 在业务连接池容量为 1 时通过，验证独立锁连接不阻塞业务 SQL，等待取消后可重新取得锁。
- 执行鉴权：Docker/OpenSandbox 真实实例验证 execd 独立令牌、匿名命令拒绝、两电脑三卷共享、暂停恢复及重建后持久数据保留。
- 集成回归：确定性模型驱动的 HTTP 测试验证同机多会话并行、并行子任务、取消互不影响及技能版本切换；电脑管理、文件预览、保存到存储已通过浏览器验证。远程候选镜像、完整回归结果、截图与已知限制单独保存在忽略的操作记录中，不将环境地址或凭据写入设计文档。
- T3 电脑恢复：`TestRecoverComputerProvenanceIntegration` 与 `TestRecoverComputerInFlightAcquireIntegration` 在真实 MySQL 通过，覆盖未知同步归属、历史无归属计划保守阻塞、跨电脑/代次/fence 隔离，以及安装在途时旧运行终结后不重建租约。
- T6：`TestMigrationPythonSafetyIntegration`、`TestMigrationMySQLIntegration`、`TestMigrationSourceMutationBlocksBindingIntegration` 在隔离 Linux Python 3.12 与 MySQL 8 通过，覆盖权限/链接/摘要、默认目录冲突、只读盘点无写入、丢失导入响应后的重试和源变更拒绝绑定。集成入口为 `WISPJAR_MIGRATION_FS_TEST=1`、`WISPJAR_MIGRATION_TEST_DSN`；仅允许专用已迁移测试库。测试使用真实文件系统和 SQL，但旧实例 metadata 与目标电脑供应为测试适配器，不能替代真实 OpenSandbox 旧实例迁移验收。
