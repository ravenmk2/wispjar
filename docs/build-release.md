# 构建与发布

## 本地构建

- 统一入口为 `scripts/build.sh`，Windows 下通过 Git Bash 执行。产物位于 `dist/`，覆盖 Linux、macOS、Windows 的 amd64 与 arm64。
- 使用 `CGO_ENABLED=0`；版本通过 `-ldflags "-s -w -X main.version=<ver>"` 注入，`main.version` 默认值为 `dev`。构建脚本从 Git 获取版本。
- `web/dist/` 整体忽略，通过 `go:embed` 嵌入二进制。仅做后端编译、lint 或测试时，可在仓库根目录创建占位文件：

  ```bash
  mkdir -p web/dist && touch web/dist/index.html
  ```

  `scripts/build.sh` 已内置占位逻辑，并保留已有前端产物；需要可用界面时，先在 `web/` 中执行 `pnpm install --frozen-lockfile` 和 `pnpm build`。

## 从源码构建 Docker 镜像

仓库根目录的 `Dockerfile` 使用多阶段构建：Node + pnpm 构建前端 → Go 构建二进制 → Alpine 运行时。模块下载使用 `GOPROXY=https://goproxy.cn,direct`。

构建上下文必须为仓库根目录：

```bash
docker build --build-arg version=<ver> -t wispjar:<ver> .
```

## 正式发布

- 推送 `v*.*.*` tag 触发 `.github/workflows/release.yml`，由 GoReleaser 生成发布产物。
- 工作流必须先构建真实 `web/dist/`，再运行 GoReleaser，保证二进制嵌入完整界面；发布流程不能改回占位文件。
- 发布镜像使用 `.goreleaser.yml` 的 `dockers_v2` 与根目录 `Dockerfile.release`。该 Dockerfile 只组装 Alpine 运行时和预编译二进制，不能替换为源码构建用的多阶段 `Dockerfile`。
- GoReleaser 提供的最小构建上下文仅包含 Dockerfile 和 `linux/<arch>/` 下的二进制，复制路径必须与目标平台匹配：

  ```dockerfile
  ARG TARGETPLATFORM
  COPY ${TARGETPLATFORM}/wispjar /usr/local/bin/wispjar
  ```

- 保留 `<version>`、`latest` 多架构标签，以及 `<version>-amd64`、`<version>-arm64` 兼容标签。
- changelog 从 Conventional Commits 自动生成，不维护 `CHANGELOG.md`。
