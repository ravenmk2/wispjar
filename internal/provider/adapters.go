package provider

import (
	"encoding/json"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

// 供应商适配器注册表（runtime.md §3）：把各家参数映射与 usage 字段差异
// 收敛为按 provider_type 分派的显式适配器，openai_compat 为兜底（宽容解析、
// 默认「未上报」），新增供应商零适配可跑。

// AdapterCapabilities 是供应商能力声明（参数放行、usage 字段口径、缓存命中粒度）。
type AdapterCapabilities struct {
	// UsageCacheReadField 是缓存读（命中）字段的口径标识（点路径，仅作文档/展示用）。
	UsageCacheReadField string
	// UsageCacheWriteField 是缓存写字段口径；该家无写入语义时为 ""。
	UsageCacheWriteField string
	// SupportsPromptCacheKey 该家是否放行 prompt_cache_key 路由亲和参数。
	SupportsPromptCacheKey bool
	// MinCacheTokens 最小可命中前缀粒度（ark/qwen 1024；deepseek 落盘粒度不透明取 0）。
	MinCacheTokens int
}

// ProviderAdapter 按 provider_type 承接参数映射与原始 usage 归一化。
type ProviderAdapter interface {
	Capabilities() AdapterCapabilities
	// ApplyParams 把合并后的参数白名单映射到 openai 组件配置（未识别键忽略）。
	ApplyParams(cfg *openai.ChatModelConfig, params map[string]any)
	// NormalizeUsage 从原始 usage JSON 提取缓存读/写 tokens；
	// raw 里没有该家任何缓存字段时 reported=false（区分「未命中」与「未上报」）。
	NormalizeUsage(raw map[string]any) (cacheRead, cacheWrite int64, reported bool)
}

// AdapterFor 返回 provider_type 对应适配器；未知类型回退 openai_compat 兜底。
func AdapterFor(providerType string) ProviderAdapter {
	switch providerType {
	case "deepseek":
		return deepseekAdapter{}
	case "ark":
		return arkAdapter{}
	case "qwen":
		return qwenAdapter{}
	default:
		return compatAdapter{}
	}
}

// applyCommonParams 三家通用：temperature/top_p/max_tokens 白名单映射。
func applyCommonParams(cfg *openai.ChatModelConfig, params map[string]any) {
	if v, ok := numberParam(params, "temperature"); ok {
		f32 := float32(v)
		cfg.Temperature = &f32
	}
	if v, ok := numberParam(params, "top_p"); ok {
		f32 := float32(v)
		cfg.TopP = &f32
	}
	if v, ok := numberParam(params, "max_tokens"); ok {
		n := int(v)
		cfg.MaxTokens = &n
	}
}

// applyReasoningEffort 原生支持系统档位的 API：保留值，由目标模型执行其兼容映射。
func applyReasoningEffort(cfg *openai.ChatModelConfig, params map[string]any) {
	if v, ok := stringParam(params, "reasoning_effort"); ok && isReasoningEffort(v) {
		cfg.ReasoningEffort = openai.ReasoningEffortLevel(v)
	}
}

// standardCacheRead ark/OpenAI/compat 的缓存读口径：prompt_tokens_details.cached_tokens。
func standardCacheRead(raw map[string]any) (int64, bool) {
	return usageInt(usageDetails(raw), "cached_tokens")
}

// ---------- deepseek ----------

type deepseekAdapter struct{}

func (deepseekAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		UsageCacheReadField: "prompt_cache_hit_tokens",
		MinCacheTokens:      0,
	}
}

func (deepseekAdapter) ApplyParams(cfg *openai.ChatModelConfig, params map[string]any) {
	applyCommonParams(cfg, params)
	if v, ok := stringParam(params, "reasoning_effort"); ok {
		applyDeepSeekEffort(cfg, v)
	}
}

// NormalizeUsage deepseek 权威字段是顶层 prompt_cache_hit_tokens（官方承诺），
// 生产实测同时返回 prompt_tokens_details.cached_tokens（未文档化），作 fallback。
func (deepseekAdapter) NormalizeUsage(raw map[string]any) (int64, int64, bool) {
	if v, ok := usageInt(raw, "prompt_cache_hit_tokens"); ok {
		return v, 0, true
	}
	if v, ok := standardCacheRead(raw); ok {
		return v, 0, true
	}
	return 0, 0, false
}

// ---------- ark ----------

type arkAdapter struct{}

func (arkAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		UsageCacheReadField:    "prompt_tokens_details.cached_tokens",
		SupportsPromptCacheKey: true,
		MinCacheTokens:         1024,
	}
}

func (a arkAdapter) ApplyParams(cfg *openai.ChatModelConfig, params map[string]any) {
	applyCommonParams(cfg, params)
	// 方舟支持选档的模型接受全部七档，由 API 按模型版本转换等效值；
	// ep-* 接入点隐藏实际模型，不能在客户端统一压到 high 或猜测开关。
	// https://docs.volcengine.com/docs/ark/deep-thinking
	applyReasoningEffort(cfg, params)
	// prompt_cache_key 仅 ark 放行：上下文缓存路由亲和参数（builder 注入 sessionID）。
	if a.Capabilities().SupportsPromptCacheKey {
		if v, ok := stringParam(params, "prompt_cache_key"); ok {
			setExtraField(cfg, "prompt_cache_key", v)
		}
	}
}

func (arkAdapter) NormalizeUsage(raw map[string]any) (int64, int64, bool) {
	if v, ok := standardCacheRead(raw); ok {
		return v, 0, true
	}
	return 0, 0, false
}

// ---------- qwen ----------

type qwenAdapter struct{}

func (qwenAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		UsageCacheReadField:  "prompt_tokens_details.cached_tokens",
		UsageCacheWriteField: "prompt_tokens_details.cache_creation_input_tokens",
		MinCacheTokens:       1024,
	}
}

// ApplyParams qwen 特化：按模型版本使用原生档位或 enable_thinking/thinking_budget。
func (qwenAdapter) ApplyParams(cfg *openai.ChatModelConfig, params map[string]any) {
	applyCommonParams(cfg, params)
	if v, ok := stringParam(params, "reasoning_effort"); ok {
		applyQwenEffort(cfg, v)
	}
}

func (qwenAdapter) NormalizeUsage(raw map[string]any) (int64, int64, bool) {
	details := usageDetails(raw)
	read, rok := usageInt(details, "cached_tokens")
	write, wok := usageInt(details, "cache_creation_input_tokens")
	return read, write, rok || wok
}

// ---------- openai_compat（兜底） ----------

type compatAdapter struct{}

func (compatAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		UsageCacheReadField: "prompt_tokens_details.cached_tokens",
		MinCacheTokens:      0,
	}
}

func (compatAdapter) ApplyParams(cfg *openai.ChatModelConfig, params map[string]any) {
	applyCommonParams(cfg, params)
	applyReasoningEffort(cfg, params)
	// 兜底不放行 prompt_cache_key（openai_compat 是开放集合，语义不可枚举）。
}

func (compatAdapter) NormalizeUsage(raw map[string]any) (int64, int64, bool) {
	if v, ok := standardCacheRead(raw); ok {
		return v, 0, true
	}
	return 0, 0, false
}

// ---------- raw usage 取值工具（容忍畸形/缺失） ----------

// usageDetails 取 raw["prompt_tokens_details"]；缺失或非对象返回 nil。
func usageDetails(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	m, _ := raw["prompt_tokens_details"].(map[string]any)
	return m
}

// usageInt 宽容取整：JSON 数字解为 float64，也容忍 int/json.Number；键不存在返回 false。
func usageInt(m map[string]any, key string) (int64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case float32:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}
