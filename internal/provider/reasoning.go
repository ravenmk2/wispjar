package provider

import (
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

// 平台档位保存在 capabilities/session.model_params，供应商参数仅在调用边界转换。
func isReasoningEffort(effort string) bool {
	switch effort {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	}
	return false
}

// DeepSeek Chat API 的思考开关独立于档位；不能把 none 发到 reasoning_effort。
// https://api-docs.deepseek.com/guides/thinking_mode/
func applyDeepSeekEffort(cfg *openai.ChatModelConfig, effort string) {
	if !isReasoningEffort(effort) {
		return
	}
	if effort == "none" {
		setExtraField(cfg, "thinking", map[string]any{"type": "disabled"})
		return
	}
	setExtraField(cfg, "thinking", map[string]any{"type": "enabled"})
	switch effort {
	case "minimal", "low":
		cfg.ReasoningEffort = "low"
	case "medium", "high", "xhigh":
		cfg.ReasoningEffort = "high"
	case "max":
		cfg.ReasoningEffort = "max"
	}
}

// Qwen3.8 提供原生 low/medium/xhigh；更早的 Qwen 用连续 thinking_budget。
// 预算数值是平台分档约定，不是供应商提供的枚举。max 不设预算，使用模型最大值。
// https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions/
func applyQwenEffort(cfg *openai.ChatModelConfig, effort string) {
	if !isReasoningEffort(effort) {
		return
	}
	if effort == "none" {
		setExtraField(cfg, "enable_thinking", false)
		return
	}
	setExtraField(cfg, "enable_thinking", true)
	if strings.HasPrefix(strings.ToLower(cfg.Model), "qwen3.8-") {
		switch effort {
		case "minimal", "low":
			cfg.ReasoningEffort = "low"
		case "medium":
			cfg.ReasoningEffort = "medium"
		case "high", "xhigh", "max":
			cfg.ReasoningEffort = "xhigh"
		}
		return
	}
	budgets := map[string]int{
		"minimal": 128,
		"low":     1024,
		"medium":  8192,
		"high":    16384,
		"xhigh":   32768,
	}
	if budget, ok := budgets[effort]; ok {
		setExtraField(cfg, "thinking_budget", budget)
	}
}
