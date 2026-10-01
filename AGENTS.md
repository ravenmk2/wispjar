# AI Agents 工作规范

WispJar：多租户智能体平台（服务端应用，Go 实现）。

## 目录结构

```txt
wispjar/
├── cmd/wispjar/      # 程序入口，只做组装与启动，不含业务逻辑
├── internal/        # 应用私有代码（按业务域分层，需求确定后补充）
├── web/             # 前端源码与构建产物（embed 进二进制，dist/ 不入库）
├── scripts/         # 辅助脚本（build.sh 本地多平台构建）
└── .github/workflows/  # CI（test.yml）与发布（release.yml，tag v*.*.* 触发 goreleaser）
```

## 文档索引

文档变化时同步更新

- （暂无，后续文档按 <路径>：<一句话说明> 格式补充）

## 工程约定

- CGO_ENABLED=0 纯 Go 构建；版本经 `-ldflags "-s -w -X main.version=<ver>"` 注入，`main.version` 默认 `dev`
- 本地构建统一入口 `scripts/build.sh`（Windows 下经 Git Bash 执行）；正式发布走 goreleaser
- `web/dist/` 整体忽略，本地编译前需 `mkdir -p web/dist && touch web/dist/index.html`（`scripts/build.sh` 已内置该逻辑）
- changelog 由 goreleaser 从 Conventional Commits 生成，不维护 CHANGELOG.md
