package ws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/runtime"
)

type followOpenPayload struct {
	OrgID     string `json:"orgId"`
	SessionID string `json:"sessionId"`
	AfterSeq  int64  `json:"afterSeq"`
}

type snapshotValue struct {
	Type string `json:"type"`
	*runtime.Snapshot
}

type approvalResponseUplink struct {
	Type     string `json:"type"`
	CallID   string `json:"callId"`
	Decision string `json:"decision"`
}

func (c *conn) openFollow(f clientFrame) {
	var p followOpenPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil || p.OrgID == "" || p.SessionID == "" {
		c.sendFrame(errorFrame(f.StreamID, respond.CodeInvalid, "payload 需要 orgId 与 sessionId", nil))
		c.sendFrame(endFrame(f.StreamID))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	member, err := c.server.deps.Store.IsMember(ctx, p.OrgID, c.uid)
	if err != nil {
		c.failOpen(f.StreamID, err)
		return
	}
	if !member {
		c.sendFrame(errorFrame(f.StreamID, respond.CodeForbidden, "不是该组织成员", nil))
		c.sendFrame(endFrame(f.StreamID))
		return
	}
	owned, err := c.server.deps.Store.OwnsSession(ctx, p.OrgID, c.uid, p.SessionID)
	if err != nil {
		c.failOpen(f.StreamID, err)
		return
	}
	if !owned {
		c.sendFrame(errorFrame(f.StreamID, "session/not_found", "会话不存在或无权访问", nil))
		c.sendFrame(endFrame(f.StreamID))
		return
	}
	snapshot, frames, unsubscribe, err := c.server.deps.Orchestrator.Follow(p.SessionID, 0)
	if err != nil {
		c.failOpen(f.StreamID, err)
		return
	}
	st := &stream{id: f.StreamID, cancel: unsubscribe}
	st.uplink = func(value json.RawMessage) { c.followUplink(f.StreamID, p.SessionID, value) }
	c.addStream(st)
	c.sendFrame(itemFrame(f.StreamID, snapshotValue{Type: "snapshot", Snapshot: snapshot}))
	go c.forwardFollow(f.StreamID, frames)
}

func (c *conn) forwardFollow(streamID string, frames <-chan runtime.FollowFrame) {
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				c.closeStream(streamID, true)
				return
			}
			if v := frame.MarshalValue(); v != nil {
				c.sendFrame(itemFrame(streamID, v))
			}
		case <-c.done:
			return
		}
	}
}

func (c *conn) followUplink(streamID, sessionID string, value json.RawMessage) {
	var up approvalResponseUplink
	if err := json.Unmarshal(value, &up); err != nil || up.Type == "" {
		c.sendFrame(errorFrame(streamID, respond.CodeInvalid, "无法解析 uplink 消息", nil))
		return
	}
	switch up.Type {
	case "approval-response":
		if up.CallID == "" || (up.Decision != "allow" && up.Decision != "deny") {
			c.sendFrame(errorFrame(streamID, respond.CodeInvalid, "approval-response 需要 callId 与 decision(allow|deny)", nil))
			return
		}
		if err := c.server.deps.Orchestrator.DecideApproval(sessionID, up.CallID, up.Decision == "allow", c.uid); err != nil {
			code, msg := streamErrCode(err)
			c.sendFrame(errorFrame(streamID, code, msg, nil))
		}
	default:
		c.sendFrame(errorFrame(streamID, respond.CodeInvalid, "未知 uplink 类型: "+up.Type, nil))
	}
}
