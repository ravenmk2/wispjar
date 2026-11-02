# 模型请求日志

仅供系统管理员调试真实模型输入。与 session、session_event、usage 和工具执行记录独立，不记录响应。默认关闭，不以日志成功作为模型运行前提。

## 捕获与开关

- 系统配置为 `platform_setting.model_request_log`，默认 `{"enabled":false,"retention_days":7,"max_storage_bytes":5368709120}`。模型开关保存在 `model_request_log_setting`，缺行等同关闭。
- 双开关同时开启才捕获。启动加载不可变内存快照；配置更新串行写库成功后原子发布，请求路径不查数据库。开关影响下一次请求，已入队的日志允许写完。
- HTTP RoundTripper 包装 Chat Completions 与 Responses，在底层读取 Body 时旁路复制原始字节，不预读、不重建 JSON、不修改原请求。文件上传等非推理路径不捕获；显式重试再次经过 transport 时独立记录。
- 记录只表示客户端发送边界读取到完整 payload，不证明供应商接收成功。HTTP 错误也可有完整记录；读取中断不保存残缺正文。所有认证请求头不保存，URL 去掉 userinfo、query 和 fragment。正文原样保留，包括敏感内容和媒体 base64。

## 异步与存储

- 请求侧仅读取快照、申请额度、复制字节并非阻塞入队，不做数据库/文件 I/O，不等待 worker。
- 单请求最多 32 MiB；捕获、排队和正在写入的 payload 预留内存合计最多 128 MiB；队列最多 128 条。未知长度先预留 32 MiB。超限、队列满和读取不完整分别计数，原始请求继续透传。
- 单后台 worker 写文件与数据库，失败最多尝试三次。统计自进程启动累计，重启归零；存储错误进入服务日志。正常停机最多等待 10 秒排空；异常退出允许丢失尚未持久化记录。
- 文件位于 `<storage.data_dir>/model-request-logs/YYYY-MM-DD/<id>.json`，日期按请求开始时间 UTC+08:00。写同目录 `.tmp`、关闭后原子 rename；数据库保存相对路径、字节数、SHA-256 和元信息。文件 0600、目录 0700，不公开静态目录。
- 每分钟清理到期记录并恢复未完成状态；写入前清理最旧记录满足容量。单请求超过总容量则保存失败；磁盘不可用时清理无法保证，但不阻断智能体。

## 数据与恢复

迁移 `000013_model_request_log` 只新增两表，无外键、无会话字段，兼容旧应用。完整 DDL 见对应迁移。

| 表 | 字段 |
|---|---|
| model_request_log_setting | model_id 主键，enabled，updated_at |
| model_request_log | id 主键，started_at，model_id，provider_id，status，payload_path，payload_bytes，payload_sha256，metadata JSON，error_detail JSON，updated_at |

metadata 保存当时的模型名/代码、供应商名/类型、协议、方法、地址、Content-Type；改名不影响旧日志。索引 `(started_at,id)`、`(model_id,started_at,id)`、`(status,updated_at,id)`，列表用 UUIDv7 主键游标。

worker 先创建 writing 索引，再写文件、rename，最后更新 ready；写入失败为 failed。恢复时校验 writing 最终文件的大小和 SHA-256，完整则 ready，否则 failed。请求日志属于物理删除例外：先批量标记 deleting，再逐条删除文件和索引；维护任务重试未完成清理。超过一小时的遗留临时文件和无索引文件会回收。

清空设置请求开始时间截止点，后台删除之前的记录，尚在捕获或队列中的旧请求也丢弃；之后的新请求保留。不改变开关或任何会话数据。

## 管理 API 与界面

全部端点以 `/api/v1/platform` 为前缀，经 `RequirePlatformAdmin` 授权。

| 方法 | 路径 | 行为 |
|---|---|---|
| GET | /request-logs/config | 配置、模型开关 map、进程统计及 clearing |
| PUT | /request-logs/config | `{enabled,retention_days,max_storage_bytes}`；1–90 天，1 MiB–100 GiB |
| PUT | /request-logs/models/:id | `{enabled}`，要求模型存在 |
| GET | /request-logs | model_id 可选，before 为 ID 游标；最多 50 条，items / next_cursor |
| GET | /request-logs/:id/payload | ready 才可读取，原始 JSON 附件、no-store；404 不存在，409 未 ready |
| DELETE | /request-logs | 202，安排后台清空 |

入口：系统管理 → 模型服务 → 请求日志。总开关收进页头；请求记录、模型开关、存储与清理三个标签。详情展示文件路径、摘要及原始/格式化 JSON，支持复制和下载。大于 200,000 字符时不展开高亮，仍可复制/下载全文；格式化不改变存储或下载字节。

当前只支持单实例。多实例需要共享文件存储、配置广播及清理协调，不能直接让多个实例操作同一日志根目录。
