package runtime

import (
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/session"
)

// 用量回收（runtime.md §11）：从流式 chunk 的 ResponseMeta.Usage 提取，
// 写入 assistant/message 事件 data.usage（usage_daily 聚合属 M4）。

func streamUsage(u *schema.TokenUsage) *StreamUsage {
	if u == nil {
		return nil
	}
	return &StreamUsage{
		InputTokens:     int64(u.PromptTokens),
		OutputTokens:    int64(u.CompletionTokens),
		CacheReadTokens: int64(u.PromptTokenDetails.CachedTokens),
		ReasoningTokens: int64(u.CompletionTokensDetails.ReasoningTokens),
	}
}

func eventUsage(u *schema.TokenUsage) *session.Usage {
	if u == nil {
		return nil
	}
	return &session.Usage{
		InputTokens:     int64(u.PromptTokens),
		OutputTokens:    int64(u.CompletionTokens),
		CacheReadTokens: int64(u.PromptTokenDetails.CachedTokens),
		ReasoningTokens: int64(u.CompletionTokensDetails.ReasoningTokens),
	}
}
