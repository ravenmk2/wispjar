package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

// Snapshot 是 session.follow 首帧（protocol.md §4.2）。
type Snapshot struct {
	Header          SnapshotHeader  `json:"header"`
	Records         []session.Event `json:"records"`
	HasMore         bool            `json:"hasMore"`
	AssistantStream *SnapshotStream `json:"assistantStream,omitempty"`
}

type SnapshotHeader struct {
	ComputerID          string        `json:"computerId,omitempty"`
	ComputerStatus      string        `json:"computerStatus,omitempty"`
	WorkspaceDir        string        `json:"workspaceDir,omitempty"`
	ModelSettingsLocked bool          `json:"modelSettingsLocked"`
	SessionID           string        `json:"sessionId"`
	Title               string        `json:"title"`
	TitlePinned         bool          `json:"titlePinned"`
	AgentID             string        `json:"agentId"`
	AgentName           string        `json:"agentName"`
	AgentAvatar         string        `json:"agentAvatar"`
	ModelID             string        `json:"modelId"`
	ApprovalMode        string        `json:"approvalMode"`
	Running             bool          `json:"running"`
	LastSeq             int64         `json:"lastSeq"`
	UsageTotal          session.Usage `json:"usageTotal"`
	CreatedAt           time.Time     `json:"createdAt"`
}

type SnapshotStream struct {
	AttemptID       string        `json:"attemptId"`
	Turn            int           `json:"turn"`
	Step            int           `json:"step"`
	StartedAfterSeq int64         `json:"startedAfterSeq"`
	CompactChunks   []StreamChunk `json:"compactChunks"`
	NextIndex       int           `json:"nextIndex"`
}

const snapshotWindow = 200

// Follow 返回快照 + 增量订阅句柄（供 gateway session.follow 使用）。
func (o *orchestrator) Follow(sessionID string, afterSeq int64) (*Snapshot, <-chan FollowFrame, func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := o.deps.Sessions.SessionByID(ctx, sessionID)
	if err != nil {
		return nil, nil, nil, err
	}
	started, err := o.deps.Events.HasUserMessage(ctx, sessionID)
	if err != nil {
		return nil, nil, nil, err
	}
	var rows []session.Event
	hasMore := false
	if afterSeq > 0 {
		raw, err := o.deps.Events.Events(ctx, sessionID, afterSeq, 0)
		if err != nil {
			return nil, nil, nil, err
		}
		rows = toEvents(raw)
	} else {
		raw, err := o.deps.Events.TailEvents(ctx, sessionID, snapshotWindow)
		if err != nil {
			return nil, nil, nil, err
		}
		rows = toEvents(raw)
		hasMore = len(rows) > 0 && rows[0].Seq > 1
	}

	var snap agentSnapLite
	_ = json.Unmarshal(sess.AgentSnapshot, &snap)
	usage := session.Usage{}
	_ = json.Unmarshal(sess.UsageTotal, &usage)

	snapshot := &Snapshot{
		Header: SnapshotHeader{
			ModelSettingsLocked: started,
			SessionID:           sess.ID,
			Title:               sess.Title,
			TitlePinned:         sess.TitlePinned,
			AgentID:             sess.AgentID,
			AgentName:           snap.Name,
			AgentAvatar:         snap.Avatar,
			ModelID:             sess.ModelID,
			ApprovalMode:        sess.ApprovalMode,
			Running:             sess.Running,
			LastSeq:             sess.LastSeq,
			UsageTotal:          usage,
			CreatedAt:           sess.CreatedAt,
		},
		Records: rows,
		HasMore: hasMore,
	}
	if sess.ComputerID != nil {
		snapshot.Header.ComputerID = *sess.ComputerID
		snapshot.Header.WorkspaceDir = session.WorkspaceDir(sess.ID)
		if o.deps.Computers != nil {
			if computer, err := o.deps.Computers.GetOwned(ctx, sess.OrgID, sess.UserID, *sess.ComputerID); err == nil {
				snapshot.Header.ComputerStatus = computer.Status
			}
		}
	}

	o.mu.RLock()
	if rs := o.runs[sessionID]; rs != nil && rs.attempt != nil {
		snapshot.AssistantStream = &SnapshotStream{
			AttemptID:       rs.attempt.id,
			Turn:            rs.attempt.turn,
			Step:            rs.attempt.step,
			StartedAfterSeq: rs.lastSeq,
			CompactChunks:   append([]StreamChunk{}, rs.attempt.chunks...),
			NextIndex:       rs.attempt.next,
		}
	}
	o.mu.RUnlock()

	ch, unsubscribe := o.deps.Bus.SubscribeSession(sessionID, 256)
	return snapshot, ch, unsubscribe, nil
}

type agentSnapLite struct {
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}

func toEvents(raw []store.SessionEvent) []session.Event {
	out := make([]session.Event, 0, len(raw))
	for _, r := range raw {
		out = append(out, session.Event{Seq: r.Seq, Type: r.Type, Time: r.TimeMs, Data: r.Data})
	}
	return out
}
