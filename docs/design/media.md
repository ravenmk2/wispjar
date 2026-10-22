# 图片工具与供应商文件引用

本文规定 read_media、历史图片与供应商 Files API 的首期实现；运行时入口见 [runtime.md](runtime.md)。

## 1. 范围与能力

首期支持 DeepSeek、OpenAI-compatible、方舟的 Responses 协议。模型显式配置 `capabilities.api_protocol=responses`、`vision=true`、`tools=true`、`tool_images=true` 后才允许 read_media 读取；未知能力或仅文本工具结果的协议保留拒绝桩，调用在任何图片 I/O 前返回可恢复错误文本，不追加 user 图片消息，不自动回退 URL/base64。现有 Chat Completions 模型保持原协议，用户上传图片在请求边界内联。Claude Messages、Gemini、Kimi 的原生适配留待后续，不能因供应商有 Files API 就自动开放工具图片。

Responses 的工具输出为 `function_call_output.output[]`，图片使用 `input_image.file_id`。供应商和协议分别决定上传参数、文件状态、有效期、推理参数及错误分类；管理员应仅对已经验证的模型启用工具图片。方舟的模型版本限制仍需遵守供应商文档。

## 2. 持久化

图片字节先写本地不可变内容寻址存储，再提交 tool/result 事件。事件仍使用 `content + attachments`，只保存平台附件 ID、MIME、尺寸、大小和名称。远端 file_id、签名 URL、base64 不进入事件；运行中的工具图片和新 checkpoint 也只保留平台引用。原文件变化、云电脑重建不影响已提交历史。

物理 blob 按 SHA-256 去重，租户附件授权独立于首个上传者。增加租户附件关系，旧 attachment 行迁移为所属租户的关系。按引用读取必须先验证租户权限。read_media 接受 `path` 或 `attachment_id`，两者互斥；后者用于从持久附件恢复卸载图片，不依赖原路径。

供应商上传映射独立持久化，键包含租户、连接作用域（供应商、endpoint、凭据指纹、媒体策略版本）和图片内容标识。只持久化可用文件的远端 ID 和过期时间，处理中状态仅用于有界轮询；它是可重建缓存，不是历史事实。凭据轮换或切换供应商不能复用旧作用域的文件。原图保持不变；首期不自动缩放、重编码，后续转换须以原图 hash、转换版本和参数确定独立版本。

Responses assistant 输出额外保存 `provider_replay`（连接作用域、模型与原生 output 的 JSON 字符串），用于完整回放 function_call ID、明文/加密 reasoning；只有同连接、同模型复用，跨供应商回退通用 assistant/tool-call 内容，不把供应商推理状态发给其他连接。此字段不含输入图片、远端文件 ID 或密钥。

## 3. 请求与恢复

每次模型请求（含轮内继续、子智能体、审批恢复）都先解析引用并检查预算，然后按需上传、等待 active、使用 file_id。相同租户和连接下的同图复用映射；并发上传合并。有效期临近时刷新，收到明确的文件失效/不存在错误时删除对应失效映射并至多重建重试一次；认证、额度、模型和格式错误直接报告，不能当作过期无限重试。请求取消必须取消上传、轮询与模型流。

图片预算覆盖本次所有 user/tool 图片；超过图片数量或字节预算时按确定性的最旧优先规则投影为占位文本，原始引用保持不变。占位包含 attachment_id 及 read_media 重读提示。请求策略版本 `file-reference-v1` 记录在本地 `request/header` 事件的 `image_offload` 字段，并参与供应商文件映射缓存作用域计算，不通过自定义 HTTP 请求头传给供应商；同一已提交输入及策略产生同一投影。file_id 更换不改事件，也不承诺供应商 prompt cache 跨 file_id 命中。

已有带图历史切换到不支持工具图片的路由时拒绝；旧事件无需重写，可从本地 blob 重新上传。上线前等待运行会话空闲；新版本不依赖旧 checkpoint 中远端文件引用。文件丢失或校验失败明确失败，不能静默以文本代替本应发送的图片。

## 4. 生命周期与成本

DeepSeek/OpenAI 上传使用 user_data、24 小时有效期，提前 5 分钟刷新；方舟使用 user_data、等待 active，遵守返回的 expires_at，不发送其未声明支持的 expires_after。没有统一永久 file_id 承诺。上传/就绪轮询最多 2 分钟；同进程并发上传按作用域合并，多实例可能重复上传，但不会跨租户复用。本地附件不随会话删除，引用感知 GC 后续实施。file_id 减少重复上传流量，不等于减少视觉 token，也不保证免预处理或命中模型缓存。

## 5. 验收

- 请求结构：图片进入原生 function_call_output，call_id 完整，绝不补 user 消息；纯文本路由拒绝。
- 上传：同图复用、不同租户/凭据隔离、active 轮询、取消、过期重建、失效最多重试一次。
- 历史：原文件修改/删除后仍恢复原图；事件和工具 checkpoint 没有 base64/file_id；旧事件可读。
- 多轮：轮内连续读图也受预算约束；工具图片及普通上传图片共用预算。
- 流式：文本、工具调用、推理与 usage 正确，断流不能提交完整成功结果。
- 部署：纯 Go 测试、vet、前端测试构建通过，验证旧数据保留及真实供应商受控图片识别。

## 6. 文档依据（2026-10-22）

- [OpenAI Responses](https://developers.openai.com/api/reference/resources/responses/methods/create)
- [DeepSeek Responses](https://api-docs.deepseek.com/guides/responses_api/) 与 [Files](https://api-docs.deepseek.com/guides/files_api/)
- [方舟 Responses](https://docs.volcengine.com/docs/ark/create-model-responses-api?lang=zh) 与 [Files](https://docs.volcengine.com/docs/ark/file-api?lang=zh)
- [Claude Files](https://platform.claude.com/docs/en/build-with-claude/files)、[Gemini Files](https://ai.google.dev/gemini-api/docs/files)、[Kimi 视觉](https://platform.kimi.ai/docs/guide/use-kimi-vision-model)
