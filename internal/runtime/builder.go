package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"github.com/ravenmk2/wispjar/internal/agent"
	"github.com/ravenmk2/wispjar/internal/runtime/tools"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

type sandboxConfig struct {
	Image     string            `json:"image"`
	Resources map[string]string `json:"resources"`
	Env       map[string]string `json:"env"`
}

func parseSandboxConfig(raw json.RawMessage) sandboxConfig {
	cfg := sandboxConfig{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	return cfg
}

func parseToolsEnabled(raw json.RawMessage) map[string]bool {
	enabled := map[string]bool{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &enabled)
	}
	return enabled
}

// buildToolContext 装配一个 turn 的工具运行上下文。
func (o *orchestrator) buildToolContext(sess *store.Session, snap *agent.Snapshot) *tools.Context {
	if o.deps.ToolContextFactory != nil {
		return o.deps.ToolContextFactory(sess, snap)
	}
	sc := parseSandboxConfig(snap.SandboxConfig)
	tc := tools.NewContext(sess.ID, sess.OrgID)
	tc.UserID = sess.UserID
	tc.AgentID = sess.AgentID
	tc.Env = sc.Env
	tc.Skills = snap.Skills
	tc.Images = o.deps.ImageSaver
	tc.WebSearch = o.deps.WebSearch
	tc.PublishToolOutput = func(callID, stream, delta string) {
		o.deps.Bus.Publish(sess.ID, ToolOutputLiveFrame(callID, stream, delta))
	}
	return tc
}

func buildTools(tc *tools.Context, snap *agent.Snapshot, approvalMode string, vision bool) ([]tool.BaseTool, []string, error) {
	enabled := parseToolsEnabled(snap.ToolsEnabled)
	list, err := tools.All(enabled)
	if err != nil {
		return nil, nil, err
	}
	if enabled["web_search"] {
		ws, err := tools.WebSearch(snap.SearchProviderID)
		if err != nil {
			return nil, nil, err
		}
		list = append(list, ws)
	}
	// read_media 是 enhanced 工具（tool 消息携带图片 part），单独构建：
	// vision 门控按当前 Agent 模型在构建期捕获（主/子 Agent 各自判定）。
	if enabled["read_media"] {
		rm, err := tools.ReadMedia(vision)
		if err != nil {
			return nil, nil, err
		}
		list = append(list, rm)
	}
	if len(snap.Skills) > 0 {
		sk, err := tools.ActivateSkill()
		if err != nil {
			return nil, nil, err
		}
		list = append(list, sk)
	}
	names := make([]string, 0, len(list))
	for _, t := range list {
		info, err := t.Info(context.Background())
		if err != nil {
			return nil, nil, err
		}
		names = append(names, info.Name)
	}
	sort.Strings(names)
	return list, names, nil
}

// mergeModelParams 合并模型参数：overlay（会话级）覆盖 base（Agent 快照级）。
// factory 内再叠 model.default_params，形成 会话 > Agent > 模型默认 三级（runtime.md §2）。
func mergeModelParams(base, overlay json.RawMessage) map[string]any {
	params := map[string]any{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &params)
	}
	if len(overlay) > 0 {
		over := map[string]any{}
		if json.Unmarshal(overlay, &over) == nil {
			for k, v := range over {
				params[k] = v
			}
		}
	}
	return params
}

// sessionEffort 取会话锁定的 reasoning_effort（子智能体继承用，runtime.md §6）。
func sessionEffort(raw json.RawMessage) (string, bool) {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	s, ok := m["reasoning_effort"].(string)
	return s, ok && s != ""
}

// visionOf 查模型图片输入能力（read_media 门控；Deps.Vision 未配置时按不支持处理——
// 未知能力在文件系统与附件工作之前拒绝，对齐 DSH 的严格拒绝语义）。
func (o *orchestrator) visionOf(ctx context.Context, modelID string) bool {
	if o.deps.ToolImages == nil {
		return false
	}
	return o.deps.ToolImages(ctx, modelID)
}

// buildAgent 按会话快照构建 adk.ChatModelAgent（runtime.md §2）。
func (o *orchestrator) buildAgent(ctx context.Context, sess *store.Session, snap *agent.Snapshot, tc *tools.Context, rs *runState) (adk.Agent, []string, error) {
	modelID := sess.ModelID
	if modelID == "" {
		modelID = snap.Model.ID
	}
	params := mergeModelParams(snap.ModelParams, sess.ModelParams)
	providerType := ""
	if o.deps.ProviderType != nil {
		providerType = o.deps.ProviderType(ctx, modelID)
	} else if modelID == snap.Model.ID {
		providerType = snap.Model.ProviderType
	}
	if providerType == "ark" {
		// ark 上下文缓存路由亲和：同会话请求携带同一 prompt_cache_key（runtime.md §3）。
		params["prompt_cache_key"] = sess.ID
	}
	chatModel, err := o.deps.Models.NewChatModel(ctx, modelID, params)
	if err != nil {
		return nil, nil, err
	}
	toolList, toolNames, err := buildTools(tc, snap, sess.ApprovalMode, o.visionOf(ctx, modelID))
	if err != nil {
		return nil, nil, err
	}
	question, err := o.newQuestionTool(sess, rs, 0)
	if err != nil {
		return nil, nil, err
	}
	toolList = append(toolList, question)
	toolNames = append(toolNames, "question")
	if len(snap.SubAgents) > 0 {
		task, err := o.newTaskTool(sess, snap, tc, rs)
		if err != nil {
			return nil, nil, err
		}
		toolList = append(toolList, task)
		toolNames = append(toolNames, "task")
		sort.Strings(toolNames)
	}
	maxIter := snap.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	a, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        snap.AgentID,
		Description: snap.Description,
		Instruction: renderInstruction(snap, sess),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               toolList,
				ToolCallMiddlewares: []compose.ToolMiddleware{approvalMiddleware(sess.ApprovalMode), errorResultMiddleware()},
			},
		},
		MaxIterations: maxIter,
	})
	if err != nil {
		return nil, nil, err
	}
	return a, toolNames, nil
}

// renderInstruction 组装 system prompt。日期取会话创建日期（sess.CreatedAt）而非
// time.Now：system 是 messages[0]，逐天变化会让其后全部历史的缓存前缀失配
// （runtime.md《缓存命中优先设计原则》§2）。
func renderInstruction(snap *agent.Snapshot, sess *store.Session) string {
	date := sess.CreatedAt
	if date.IsZero() {
		date = time.Now()
	}
	var b strings.Builder
	b.WriteString(snap.SystemPrompt)
	if len(snap.Skills) > 0 {
		b.WriteString("\n\n## 可用 Skills\n")
		for _, sk := range snap.Skills {
			fmt.Fprintf(&b, "- %s: %s\n", sk.Name, sk.Description)
		}
		b.WriteString("使用 skill 工具加载某个 Skill 的完整使用说明。\n")
	}
	if len(snap.SubAgents) > 0 {
		b.WriteString("\n## 子智能体\n")
		for _, sa := range snap.SubAgents {
			fmt.Fprintf(&b, "- %s: %s\n", sa.Name, sa.Description)
		}
		b.WriteString("使用 task 工具把自包含的子任务委派给子智能体：子智能体在独立上下文中执行（看不到本会话历史），与你共享文件系统，只返回最终结果；prompt 中必须包含子智能体所需的全部信息。\n")
	}
	workDir := session.WorkspaceDir(sess.ID)
	fmt.Fprintf(&b, "\n## 运行环境\n- 当前日期：%s\n- 默认工作目录：%s；相对路径按此解析。\n",
		date.Format("2006-01-02"), workDir)
	fmt.Fprintf(&b, "- 输入目录：%s/inputs；成果目录：%s/outputs。\n- 用户共享存储：/storage；Agent文件：/agents/%s/files；只读技能：/agents/%s/skills。\n- 命令作为非root用户运行；单次命令完成或超时后会停止其后台子进程。\n", workDir, workDir, snap.AgentID, snap.AgentID)
	switch sess.ApprovalMode {
	case session.ApprovalStrict:
		b.WriteString("- 确认模式：strict。危险操作（exec/write/edit）执行前会被用户审查，可能被拒绝；被拒绝后不要反复重试同一操作。\n")
	case session.ApprovalAuto:
		b.WriteString("- 确认模式：auto。工具调用不会被人工拦截，请自行谨慎操作。\n")
	default:
		b.WriteString("- 确认模式：default。exec 命令执行前会被用户审查，可能被拒绝；被拒绝后不要反复重试同一操作。\n")
	}
	return b.String()
}
