package ws

import (
	"encoding/json"
	"errors"

	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/runtime"
	"github.com/ravenmk2/wispjar/internal/session"
)

const (
	frameOpen   = "open"
	frameItem   = "item"
	frameEnd    = "end"
	frameCancel = "cancel"
	frameErrorT = "error"

	endpointSessionFollow = "session.follow"
	endpointControl       = "control"
)

type clientFrame struct {
	Type     string          `json:"type"`
	StreamID string          `json:"streamId"`
	Endpoint string          `json:"endpoint,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
}

type serverFrame struct {
	Type     string      `json:"type"`
	StreamID string      `json:"streamId"`
	Value    any         `json:"value,omitempty"`
	Error    *frameError `json:"error,omitempty"`
}

type frameError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func itemFrame(streamID string, value any) serverFrame {
	return serverFrame{Type: frameItem, StreamID: streamID, Value: value}
}

func endFrame(streamID string) serverFrame {
	return serverFrame{Type: frameEnd, StreamID: streamID}
}

func errorFrame(streamID, code, message string, details any) serverFrame {
	return serverFrame{
		Type:     frameErrorT,
		StreamID: streamID,
		Error:    &frameError{Code: code, Message: message, Details: details},
	}
}

func (c *conn) handleMessage(data []byte) {
	var f clientFrame
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	switch f.Type {
	case frameOpen:
		c.openStream(f)
	case frameItem:
		c.mu.Lock()
		st := c.streams[f.StreamID]
		c.mu.Unlock()
		if st == nil {
			c.sendFrame(errorFrame(f.StreamID, "stream/not_found", "逻辑流不存在", nil))
			return
		}
		if st.uplink != nil {
			st.uplink(f.Value)
		}
	case frameEnd, frameCancel:
		c.closeStream(f.StreamID, false)
	}
}

func (c *conn) openStream(f clientFrame) {
	if f.StreamID == "" || f.Endpoint == "" {
		c.sendFrame(errorFrame(f.StreamID, respond.CodeInvalid, "streamId 与 endpoint 必填", nil))
		return
	}
	c.mu.Lock()
	_, dup := c.streams[f.StreamID]
	c.mu.Unlock()
	if dup {
		c.sendFrame(errorFrame(f.StreamID, "stream/conflict", "streamId 已存在", nil))
		c.sendFrame(endFrame(f.StreamID))
		return
	}
	switch f.Endpoint {
	case endpointSessionFollow:
		c.openFollow(f)
	case endpointControl:
		c.openControl(f)
	default:
		c.sendFrame(errorFrame(f.StreamID, "stream/not_found", "未知 endpoint: "+f.Endpoint, nil))
		c.sendFrame(endFrame(f.StreamID))
	}
}

func (c *conn) failOpen(streamID string, err error) {
	code, msg := streamErrCode(err)
	c.sendFrame(errorFrame(streamID, code, msg, nil))
	c.sendFrame(endFrame(streamID))
}

func streamErrCode(err error) (string, string) {
	var re *runtime.Error
	if errors.As(err, &re) {
		return re.Code, re.Message
	}
	var se *session.Error
	if errors.As(err, &se) {
		return se.Code, se.Message
	}
	return respond.CodeInternal, "服务器内部错误"
}
