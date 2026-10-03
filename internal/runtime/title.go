package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/session"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// maybeGenerateTitle 首条 user/message 后异步生成标题（runtime.md §10，失败静默）。
func (o *orchestrator) maybeGenerateTitle(sessionID string) {
	if _, loaded := o.titled.LoadOrStore(sessionID, struct{}{}); loaded {
		return
	}
	sess, err := o.deps.Sessions.SessionByID(context.Background(), sessionID)
	if err != nil || sess.Title != "" || sess.TitlePinned {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		events, err := o.deps.Events.Events(ctx, sessionID, 0, 20)
		if err != nil {
			return
		}
		content := ""
		for _, ev := range events {
			if ev.Type != session.EventUserMessage {
				continue
			}
			var d session.UserMessageData
			if jsonUnmarshal(ev.Data, &d) == nil {
				content = d.Content
			}
			break
		}
		if content == "" {
			// 纯图消息没有文本可生成标题，用静态兜底
			_, _ = o.deps.Sessions.SetTitleIfUnpinned(ctx, sessionID, "图片会话")
			return
		}
		cm, err := o.deps.Models.NewChatModel(ctx, sess.ModelID, nil)
		if err != nil {
			return
		}
		if len(content) > 2000 {
			content = content[:2000]
		}
		resp, err := cm.Generate(ctx, []*schema.Message{
			schema.UserMessage("为以下对话生成一个不超过 20 字的中文标题，只输出标题本身，不要标点结尾：\n\n" + content),
		})
		if err != nil {
			return
		}
		title := strings.TrimSpace(resp.Content)
		title = strings.Trim(title, "\"'“”‘’。！？.!? \t\n")
		if title == "" {
			return
		}
		if utf8.RuneCountInString(title) > 20 {
			r := []rune(title)
			title = string(r[:20])
		}
		ok, err := o.deps.Sessions.SetTitleIfUnpinned(ctx, sessionID, title)
		if err != nil || !ok {
			return
		}
		_, _ = o.commit(ctx, sessionID, []session.EventInput{
			{Type: session.EventSessionTitle, Data: session.SessionTitleData{Title: title, Auto: true}},
		})
	}()
}
