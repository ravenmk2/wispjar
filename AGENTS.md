# AI Agents 工作规范

Wispjar：多租户智能体平台（服务端应用，Go 实现）。

## 目录结构

```txt
wispjar/
├── cmd/wispjar/      # 程序入口，只做组装与启动，不含业务逻辑
├── internal/        # 应用私有代码（按业务域分层：config/id/store/auth/tenant/provider/
│                    #   skill/agent/runtime/sandbox/session/gateway/attachment/usage/audit，见设计文档 §4.2）
├── web/             # 前端源码与构建产物（embed 进二进制，dist/ 不入库）
├── docs/            # 设计文档
├── scripts/         # 辅助脚本（build.sh 本地多平台构建；sandbox-image/ 沙箱基础镜像 Dockerfile）
└── .github/workflows/  # CI（test.yml）与发布（release.yml，tag v*.*.* 触发 goreleaser）
```

## 文档索引

文档变化时同步更新

- docs/system-design.md：系统设计总览（需求决策、总体架构、里程碑，设计文档入口）
- docs/design/data-model.md：MySQL 完整表结构 DDL 与种子数据
- docs/design/api.md：REST API 完整规格（/api/v1）
- docs/design/protocol.md：WebSocket 实时协议（/api/ws，mux/follow/StreamChunk/事件词表）
- docs/design/runtime.md：Agent 运行时（eino 集成、工具规格、task 委派、审批、沙箱管理）
- docs/design/computers.md：用户云电脑、三类持久卷、目录与命名配置、并行运行、技能同步、迁移及实施验收任务
- docs/design/theme.md：界面主题设计 token 规范（深浅双主题色板、字体、圆角阴影与 CSS 变量映射）
- docs/design/frontend.md：前端工程与界面详细规格

## 工程约定

- CGO_ENABLED=0 纯 Go 构建；版本经 `-ldflags "-s -w -X main.version=<ver>"` 注入，`main.version` 默认 `dev`
- 本地构建统一入口 `scripts/build.sh`（Windows 下经 Git Bash 执行）；正式发布走 goreleaser
- Docker 镜像为**多阶段构建**（node+pnpm 构建 web/dist → go 构建二进制 → alpine 运行时），构建上下文为仓库根：`docker build --build-arg version=<ver> -t wispjar:<ver> .`；模块下载走 `GOPROXY=goproxy.cn`
- goreleaser release images use **`dockers_v2` + `Dockerfile.release`** at the repo root (runtime-only: alpine + prebuilt binary). The minimal build context contains the Dockerfile and binaries under `linux/<arch>/`; copy via `ARG TARGETPLATFORM` and `COPY ${TARGETPLATFORM}/wispjar ...`, not the multi-stage source Dockerfile. Keep the version and `latest` multi-arch tags plus the existing `<version>-amd64` / `<version>-arm64` compatibility tags. release.yml builds the real web/dist before goreleaser (embedded via go:embed), do not revert to the placeholder
- `web/dist/` 整体忽略，本地编译前需 `mkdir -p web/dist && touch web/dist/index.html`（`scripts/build.sh` 已内置该逻辑）
- changelog 由 goreleaser 从 Conventional Commits 生成，不维护 CHANGELOG.md

## 环境信息边界

- 入库的代码、文档、示例配置和发布文件保持环境无关，不包含实际部署主机名、IP、SSH 别名、部署路径或凭据；示例使用 localhost、回环地址或占位值。
- 实际环境资料、部署技能和操作记录集中在被 Git 与 Docker 构建上下文排除的 `.agents/` 中。运行配置使用被忽略的 `config.yaml`、本地 `.env` 文件或环境变量，不强制加入版本控制或发布产物。
