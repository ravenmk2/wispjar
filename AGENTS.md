# AI Agents 工作规范

Wispjar：Go 实现的多租户智能体平台。

## 目录结构

```txt
wispjar/
|-- cmd/wispjar/       # 组装与启动，不含业务逻辑
|-- internal/          # 按业务域组织的后端代码
|-- web/               # 前端，构建产物嵌入二进制
|-- docs/              # 设计与工程文档
|-- scripts/           # 构建脚本与沙箱镜像
`-- .github/workflows/ # CI 与发布
```

## 文档索引

文档增删或改名时，同步更新本索引与 `docs/system-design.md` 总览索引。

```txt
docs/
|-- system-design.md        # 系统设计入口
|-- build-release.md        # 构建与发布
`-- design/
    |-- data-model.md       # 表结构与种子数据
    |-- api.md              # REST API
    |-- protocol.md         # WebSocket 协议
    |-- runtime.md          # Agent 运行时
    |-- media.md            # 图片与媒体
    |-- search.md           # 网络搜索与网页读取
    |-- computers.md        # 云电脑与持久卷
    |-- sandbox-backends.md # 沙箱后端接口
    |-- theme.md            # 主题设计规范
    `-- frontend.md         # 前端工程与界面
```

## 工程约定

- 使用 `CGO_ENABLED=0` 纯 Go 构建；本地入口为 `scripts/build.sh`，Windows 下经 Git Bash 执行。
- 正式发布使用 GoReleaser；发布镜像使用 `dockers_v2` + `Dockerfile.release`，二进制必须嵌入真实前端产物。构建发布改动须遵循 [构建与发布](docs/build-release.md)。
- 提交遵循 Conventional Commits；changelog 由 GoReleaser 生成，不维护 `CHANGELOG.md`。
- Go 改动（含测试）完成后，在仓库根目录执行全量 `golangci-lint run` 和 `CGO_ENABLED=0 go test ./...`，覆盖最终代码并确认退出码为 0。交付时报告命令及结果；未执行的检查须注明。

## 环境信息边界

- 入库内容保持环境无关：不含实际部署主机名、IP、SSH 别名、路径或凭据；示例使用 localhost、回环地址或占位值。
- 环境资料、部署技能和操作记录放在 `.agents/`，由 Git 与 Docker 构建上下文排除；运行配置使用被忽略的 `config.yaml`、`.env` 文件或环境变量，不纳入发布产物。
