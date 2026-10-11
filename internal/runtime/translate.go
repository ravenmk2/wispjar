package runtime

import (
	"errors"
	"io"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/provider"
	"github.com/ravenmk2/wispjar/internal/session"
)

// framedChunk 是一个待广播的 chunk：frame index 在 attempt 内严格递增（§4.4）。
type framedChunk struct {
	index int
	chunk StreamChunk
}

// attemptState 跟踪一次流式模型调用的瞬态窗口（compactChunks 供重连快照）。
type attemptState struct {
	id       string
	turn     int
	step     int
	next     int
	blocks   []*blockState
	blockIdx map[int]*blockState // tool call index -> block
	text     *blockState
	reason   *blockState
	chunks   []StreamChunk // 压缩合并后的 chunk（连续同块 delta 合并，保序）
	usage    *schema.TokenUsage
	// captured 旁路抓取的归一化缓存用量（UsageSink 取数；nil = 未抓取/未消费）。
	captured *provider.CapturedUsage
	finish   string
}

type blockState struct {
	kind    string
	index   int
	text    string
	id      string
	name    string
	args    string
	started bool
	// startMs/lastDeltaMs 仅 reasoning 块使用：首帧与末帧 delta 时间，用于推算思考耗时
	startMs     int64
	lastDeltaMs int64
}

func newAttempt(turn, step int) *attemptState {
	return &attemptState{
		id:       id.New(),
		turn:     turn,
		step:     step,
		blockIdx: map[int]*blockState{},
	}
}

func (a *attemptState) addBlock(kind string) *blockState {
	bs := &blockState{kind: kind, index: len(a.blocks)}
	a.blocks = append(a.blocks, bs)
	return bs
}

// emit 广播序号递增；同时维护压缩后的 compactChunks。
func (a *attemptState) emit(c StreamChunk) framedChunk {
	if n := len(a.chunks); n > 0 {
		last := &a.chunks[n-1]
		if last.Type == c.Type && last.Index == c.Index {
			switch c.Type {
			case ChunkTextDelta, ChunkReasoningDelta:
				last.Text += c.Text
				return a.count(c)
			case ChunkToolCallDelta:
				if c.ID == "" {
					last.ArgumentsDelta += c.ArgumentsDelta
					return a.count(c)
				}
			}
		}
	}
	a.chunks = append(a.chunks, c)
	return a.count(c)
}

func (a *attemptState) count(c StreamChunk) framedChunk {
	fc := framedChunk{index: a.next, chunk: c}
	a.next++
	return fc
}

func (a *attemptState) blockStart(bs *blockState) framedChunk {
	bs.started = true
	return a.emit(StreamChunk{Type: ChunkBlockStart, Index: bs.index, BlockType: bs.kind})
}

// feed 把一个流式消息 chunk 翻译为 0..n 个 StreamChunk。
func (a *attemptState) feed(msg *schema.Message) []framedChunk {
	out := []framedChunk{}
	if msg.ReasoningContent != "" {
		if a.reason == nil {
			a.reason = a.addBlock(BlockReasoning)
		}
		if !a.reason.started {
			out = append(out, a.blockStart(a.reason))
		}
		if a.reason.startMs == 0 {
			a.reason.startMs = nowMs()
		}
		a.reason.lastDeltaMs = nowMs()
		a.reason.text += msg.ReasoningContent
		out = append(out, a.emit(StreamChunk{Type: ChunkReasoningDelta, Index: a.reason.index, Text: msg.ReasoningContent}))
	}
	if msg.Content != "" {
		if a.text == nil {
			a.text = a.addBlock(BlockText)
		}
		if !a.text.started {
			out = append(out, a.blockStart(a.text))
		}
		a.text.text += msg.Content
		out = append(out, a.emit(StreamChunk{Type: ChunkTextDelta, Index: a.text.index, Text: msg.Content}))
	}
	for _, tc := range msg.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		bs, ok := a.blockIdx[idx]
		if !ok {
			bs = a.addBlock(BlockToolCall)
			a.blockIdx[idx] = bs
		}
		if !bs.started {
			out = append(out, a.blockStart(bs))
		}
		delta := StreamChunk{Type: ChunkToolCallDelta, Index: bs.index}
		if tc.ID != "" && bs.id == "" {
			bs.id = tc.ID
			bs.name = tc.Function.Name
			delta.ID = tc.ID
			delta.Name = tc.Function.Name
		}
		bs.args += tc.Function.Arguments
		delta.ArgumentsDelta = tc.Function.Arguments
		if delta.ID != "" || delta.ArgumentsDelta != "" {
			out = append(out, a.emit(delta))
		}
	}
	if msg.ResponseMeta != nil {
		if msg.ResponseMeta.Usage != nil {
			a.usage = msg.ResponseMeta.Usage
		}
		if msg.ResponseMeta.FinishReason != "" {
			a.finish = msg.ResponseMeta.FinishReason
		}
	}
	return out
}

// endChunks 产出 block-end / usage / finish 收尾 chunk。
func (a *attemptState) endChunks(aborted bool) []framedChunk {
	out := []framedChunk{}
	for _, bs := range a.blocks {
		if !bs.started {
			continue
		}
		out = append(out, a.emit(StreamChunk{Type: ChunkBlockEnd, Index: bs.index, Block: bs.contentBlock()}))
	}
	if a.usage != nil {
		out = append(out, a.emit(StreamChunk{Type: ChunkUsage, Usage: streamUsage(a.usage, a.captured)}))
	}
	reason := finishReason(a.finish, len(a.blockIdx) > 0)
	if aborted {
		reason = FinishAborted
	}
	out = append(out, a.emit(StreamChunk{Type: ChunkFinish, Reason: reason}))
	return out
}

func (bs *blockState) contentBlock() session.ContentBlock {
	switch bs.kind {
	case BlockReasoning:
		block := session.ContentBlock{Type: "reasoning", Text: bs.text}
		if bs.startMs > 0 && bs.lastDeltaMs > bs.startMs {
			block.DurationMs = bs.lastDeltaMs - bs.startMs
		}
		return block
	case BlockToolCall:
		return session.ContentBlock{Type: "tool-call", ID: bs.id, Name: bs.name, Arguments: bs.args}
	default:
		return session.ContentBlock{Type: "text", Text: bs.text}
	}
}

// assistantMessage 组装完整 assistant 消息（content blocks 序 = 块出现序）。
func (a *attemptState) assistantMessage() session.AssistantMessage {
	msg := session.AssistantMessage{Role: session.RoleAssistant, Content: []session.ContentBlock{}}
	for _, bs := range a.blocks {
		if !bs.started {
			continue
		}
		msg.Content = append(msg.Content, bs.contentBlock())
	}
	return msg
}

func finishReason(raw string, hasToolCalls bool) string {
	switch raw {
	case "length", "max_tokens":
		return FinishMaxTokens
	case "tool_calls":
		return FinishToolCalls
	default:
		if hasToolCalls {
			return FinishToolCalls
		}
		return FinishStop
	}
}

// drainStream 消费整条消息流，逐 chunk 翻译并回调（广播瞬态帧）。
func (a *attemptState) drainStream(stream *schema.StreamReader[*schema.Message], onChunk func(framedChunk)) error {
	defer stream.Close()
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, fc := range a.feed(msg) {
			onChunk(fc)
		}
	}
}

func nowMs() int64 { return time.Now().UnixMilli() }
