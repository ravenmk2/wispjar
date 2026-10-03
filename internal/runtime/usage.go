package runtime

import (
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/session"
)

// 用量回收（runtime.md §11）：基础口径从流式 chunk 的 ResponseMeta.Usage 提取；
// 缓存读/写以 transport 旁路抓取的原始 usage 按供应商归一化结果为准（captured，
// eino acl 只透传标准字段，DeepSeek 顶层 hit、qwen cache_write 等会丢，见 §3）。
// captured 为 nil 或未上报时回退 eino 侧 CachedTokens（抓取失败维持原行为）。

func streamUsage(u *schema.TokenUsage, captured *provider.CapturedUsage) *StreamUsage {
	if u == nil {
		return nil
	}
	su := &StreamUsage{
		InputTokens:     int64(u.PromptTokens),
		OutputTokens:    int64(u.CompletionTokens),
		CacheReadTokens: int64(u.PromptTokenDetails.CachedTokens),
		ReasoningTokens: int64(u.CompletionTokensDetails.ReasoningTokens),
	}
	if captured != nil && captured.Reported {
		su.CacheReadTokens = captured.CacheRead
		su.CacheWriteTokens = captured.CacheWrite
		su.CacheReported = true
	}
	return su
}

func eventUsage(u *schema.TokenUsage, captured *provider.CapturedUsage) *session.Usage {
	if u == nil {
		return nil
	}
	eu := &session.Usage{
		InputTokens:     int64(u.PromptTokens),
		OutputTokens:    int64(u.CompletionTokens),
		CacheReadTokens: int64(u.PromptTokenDetails.CachedTokens),
		ReasoningTokens: int64(u.CompletionTokensDetails.ReasoningTokens),
	}
	if captured != nil && captured.Reported {
		eu.CacheReadTokens = captured.CacheRead
		eu.CacheWriteTokens = captured.CacheWrite
		eu.CacheReported = true
	}
	return eu
}
