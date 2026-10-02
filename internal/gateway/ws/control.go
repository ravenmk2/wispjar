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
	Type     string                   `json:"type"`
	Sessions []runtime.ControlSession `json:"sessions"`
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
	sessions, err := c.server.deps.Store.SessionProjections(ctx, p.OrgID, c.uid)
	if err != nil {
		c.failOpen(f.StreamID, err)
		return
	}
	if sessions == nil {
		sessions = []runtime.ControlSession{}
	}
	frames, unsubscribe := c.server.deps.Bus.SubscribeControl(p.OrgID, 64)
	c.addStream(&stream{id: f.StreamID, cancel: unsubscribe})
	c.sendFrame(itemFrame(f.StreamID, baselineValue{Type: "baseline", Sessions: sessions}))
	go c.forwardControl(f.StreamID, frames)
}

func (c *conn) forwardControl(streamID string, frames <-chan runtime.ControlFrame) {
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				c.closeStream(streamID, true)
				return
			}
			c.sendFrame(itemFrame(streamID, frame))
		case <-c.done:
			return
		}
	}
}
