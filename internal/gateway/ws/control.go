package ws

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/runtime"
)

type controlOpenPayload struct {
	OrgID string `json:"orgId"`
}

type baselineValue struct {
	Type      string                   `json:"type"`
	Sessions  []runtime.ControlSession `json:"sessions"`
	Computers []any                    `json:"computers"`
}

func (c *conn) openControl(f clientFrame) {
	var p controlOpenPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil || p.OrgID == "" {
		c.sendFrame(errorFrame(f.StreamID, respond.CodeInvalid, "payload 需要 orgId", nil))
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
		c.sendFrame(errorFrame(f.StreamID, respond.CodeForbidden, "不是该机构成员", nil))
		c.sendFrame(endFrame(f.StreamID))
		return
	}
	frames, unsubscribe := c.server.deps.Bus.SubscribeControl(p.OrgID, 64)
	sessions, err := c.server.deps.Store.SessionProjections(ctx, p.OrgID, c.uid)
	if err != nil {
		unsubscribe()
		c.failOpen(f.StreamID, err)
		return
	}
	if sessions == nil {
		sessions = []runtime.ControlSession{}
	}
	computers := []any{}
	if source, ok := c.server.deps.Store.(interface {
		ComputerProjections(context.Context, string, string) ([]any, error)
	}); ok {
		computers, err = source.ComputerProjections(ctx, p.OrgID, c.uid)
		if err != nil {
			unsubscribe()
			c.failOpen(f.StreamID, err)
			return
		}
		if computers == nil {
			computers = []any{}
		}
	}
	c.addStream(&stream{id: f.StreamID, cancel: unsubscribe})
	c.sendFrame(itemFrame(f.StreamID, baselineValue{Type: "baseline", Sessions: sessions, Computers: computers}))
	go c.forwardControl(f.StreamID, p.OrgID, frames)
}

func (c *conn) forwardControl(streamID, orgID string, frames <-chan runtime.ControlFrame) {
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				c.closeStream(streamID, true)
				return
			}
			if !c.ownsControlFrame(orgID, frame) {
				continue
			}
			c.sendFrame(itemFrame(streamID, frame))
		case <-c.done:
			return
		}
	}
}

func (c *conn) ownsControlFrame(orgID string, frame runtime.ControlFrame) bool {
	if frame.UserID != "" && frame.UserID != c.uid {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	member, err := c.server.deps.Store.IsMember(ctx, orgID, c.uid)
	if err != nil || !member {
		return false
	}
	if frame.UserID != "" {
		return true
	}
	// Older publishers lack an explicit owner. Fail closed for computers,
	// and verify session ownership instead of broadcasting to every member.
	sessionID := frame.Remove
	if frame.Session != nil {
		sessionID = frame.Session.SessionID
	}
	if sessionID == "" {
		return false
	}
	owned, err := c.server.deps.Store.OwnsSession(ctx, orgID, c.uid, sessionID)
	return err == nil && owned
}
