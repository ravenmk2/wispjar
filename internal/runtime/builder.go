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
	if o.deps.Sandbox != nil {
		tc.Sandbox = tools.WrapManager(o.deps.Sandbox)
	}
	tc.Image = sc.Image
	tc.Resources = sc.Resources
	tc.DataDir = o.deps.DataDir
	tc.Skills = snap.Skills
	tc.OnSkillUploadFailed = func(name, sandboxID string, err error) {
		o.recordSkillUploadFailure(sess.ID, name, sandboxID, err)
	}
	tc.PublishToolOutput = func(callID, stream, delta string) {
		o.deps.Bus.Publish(sess.ID, ToolOutputLiveFrame(callID, stream, delta))
	}
	return tc
}

func buildTools(tc *tools.Context, snap *agent.Snapshot, approvalMode string) ([]tool.BaseTool, []string, error) {
	list, err := tools.All(parseToolsEnabled(snap.ToolsEnabled))
	if err != nil {
		return nil, nil, err
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

// buildAgent 按会话快照构建 adk.ChatModelAgent（runtime.md §2）。
func (o *orchestrator) buildAgent(ctx context.Context, sess *store.Session, snap *agent.Snapshot, tc *tools.Context, rs *runState) (adk.Agent, []string, error) {
	modelID := sess.ModelID
	if modelID == "" {
		modelID = snap.Model.ID
	}
	params := map[string]any{}
	if len(snap.ModelParams) > 0 {
		_ = json.Unmarshal(snap.ModelParams, &params)
	}
	chatModel, err := o.deps.Models.NewChatModel(ctx, modelID, params)
	if err != nil {
		return nil, nil, err
	}
	toolList, toolNames, err := buildTools(tc, snap, sess.ApprovalMode)
	if err != nil {
		return nil, nil, err
	}
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
		Instruction: renderInstruction(snap, sess.ApprovalMode),
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               toolList,
				ToolCallMiddlewares: []compose.ToolMiddleware{approvalMiddleware(sess.ApprovalMode)},
			},
		},
		MaxIterations: maxIter,
	})
	if err != nil {
		return nil, nil, err
	}
	return a, toolNames, nil
}

func renderInstruction(snap *agent.Snapshot, approvalMode string) string {
	var b strings.Builder
	b.WriteString(snap.SystemPrompt)
	if len(snap.Skills) > 0 {
		b.WriteString("\n\n## 可用 Skills\n")
		for _, sk := range snap.Skills {
			fmt.Fprintf(&b, "- %s: %s\n", sk.Name, sk.Description)
		}
		b.WriteString("使用 activate_skill 工具加载某个 Skill 的完整使用说明。\n")
	}
	if len(snap.SubAgents) > 0 {
		b.WriteString("\n## 子智能体\n")
		for _, sa := range snap.SubAgents {
			fmt.Fprintf(&b, "- %s: %s\n", sa.Name, sa.Description)
		}
		b.WriteString("使用 task 工具把自包含的子任务委派给子智能体：子智能体在独立上下文中执行（看不到本会话历史），与你共享沙箱文件系统，只返回最终结果；prompt 中必须包含子智能体所需的全部信息。\n")
	}
	fmt.Fprintf(&b, "\n## 运行环境\n- 当前日期：%s\n- 沙箱工作目录：/workspace（exec/read/write/edit/glob/grep 工具均在沙箱内操作）\n",
		time.Now().Format("2006-01-02"))
	switch approvalMode {
	case session.ApprovalStrict:
		b.WriteString("- 确认模式：strict。危险操作（exec/write/edit）执行前会被用户审查，可能被拒绝；被拒绝后不要反复重试同一操作。\n")
	case session.ApprovalAuto:
		b.WriteString("- 确认模式：auto。工具调用不会被人工拦截，请自行谨慎操作。\n")
	default:
		b.WriteString("- 确认模式：default。exec 命令执行前会被用户审查，可能被拒绝；被拒绝后不要反复重试同一操作。\n")
	}
	return b.String()
}
