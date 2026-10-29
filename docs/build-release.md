# 构建与发布

## 本地构建

- 统一入口为 `scripts/build.sh`，Windows 下通过 Git Bash 执行。产物位于 `dist/`，覆盖 Linux、macOS、Windows 的 amd64 与 arm64。
- 使用 `CGO_ENABLED=0`；版本通过 `-ldflags "-s -w -X main.version=<ver>"` 注入，`main.version` 默认值为 `dev`。构建脚本从 Git 获取版本。
- `web/dist/` 整体忽略，通过 `go:embed` 嵌入二进制。仅做后端编译、lint 或测试时，可在仓库根目录创建占位文件：

  ```bash
  mkdir -p web/dist && touch web/dist/index.html
  ```

  `scripts/build.sh` 已内置占位逻辑，并保留已有前端产物；需要可用界面时，先在 `web/` 中执行 `pnpm install --frozen-lockfile` 和 `pnpm build`。

## MiSans 字体资源

- `pnpm dev`、`pnpm build` 显式执行字体准备脚本，不依赖包管理器的隐式生命周期钩子；也可单独执行 `pnpm fonts:prepare`。
- `web/scripts/fonts.lock.json` 固定官方资源 URL、快照标识及源包、许可、三个字体文件的 SHA-256。官方 URL 不是不可变版本地址，因此上游内容变化时校验失败，不能静默接受新版本。更新时需重新核对官方许可、资源和哈希。
- 首次联网下载官方完整压缩包（约 228 MB，十进制）及许可，缓存到 `web/.cache/fonts/`，仅提取原始 Regular/Medium/Semibold WOFF2（400/500/600，共约 14.8 MB）。不转码、裁剪或修改字体，保留内嵌版权及完整许可。源包不会进入发行产物。
- 可用 `WISPJAR_FONT_CACHE` 指定缓存目录，用 `WISPJAR_FONTS_OFFLINE=1` 禁止字体准备步骤联网。命中缓存也检查哈希；缓存缺失或损坏时，联网模式重新下载，离线模式明确失败。下载有超时、三次重试和临时文件校验，不缓存不完整结果。其他依赖仍需提前安装，字体离线模式不等于整个项目可以从零离线构建。
- 生成文件位于忽略的 `web/src/generated/fonts/`，由 Vite 输出带哈希文件名的 `assets/` 并嵌入二进制。字体缓存和生成目录均从 Git 与 Docker 构建上下文排除；登录页提供软件使用 MiSans 的声明与随包许可链接。
- CI 使用字体清单作为下载缓存键；Docker 使用 BuildKit 缓存挂载。缓存只用于加速，缺失时正常下载。部署后字体由应用同源提供，现有 `assets/` 长期 HTTP 缓存可减少重复传输；不新增 Service Worker，也不承诺浏览器完全断网可用。
- `scripts/build.sh` 仍支持后端占位构建；需要完整界面与字体的本地构建必须先执行 `pnpm build`。发布和源码 Docker 构建必须使用包含字体与许可的真实前端产物。

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
