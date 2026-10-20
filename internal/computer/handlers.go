package computer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/volume"
)

type Handler struct{ svc *Service }

func NewHandler(s *Service) *Handler { return &Handler{svc: s} }
func bind(c *gin.Context, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
func fail(c *gin.Context, err error) {
	status, code := http.StatusInternalServerError, respond.CodeInternal
	for _, entry := range []struct {
		err    error
		status int
		code   string
	}{
		{ErrNotFound, 404, "computer/not_found"}, {ErrRequired, 409, "computer/required"}, {ErrBusy, 409, "computer/busy"},
		{ErrInUse, 409, "computer/in_use"}, {ErrConflict, 409, "operation/conflict"}, {ErrInvalid, 400, "computer/invalid"},
		{ErrUnavailable, 503, "computer/unavailable"}, {volume.ErrUnavailable, 503, "computer/unavailable"},
		{volume.ErrMissing, 409, "volume/missing"}, {volume.ErrConflict, 409, "volume/conflict"},
		{store.ErrLockBusy, 409, "computer/busy"},
		{sandbox.ErrComputerInsecure, 409, "computer/rebuild_required"},
	} {
		if errors.Is(err, entry.err) {
			status, code = entry.status, entry.code
			break
		}
	}
	respond.Error(c, status, code, code, nil)
}

// View never serializes environment values, backend addresses, or operation
// plans. The original config remains server side for rebuilding.
func View(row store.Computer) any {
	var config Config
	_ = json.Unmarshal(row.Config, &config)
	detail := publicFailure(row.LastError)
	return struct {
		ID           string       `json:"id"`
		OrgID        string       `json:"org_id"`
		Name         string       `json:"name"`
		Status       string       `json:"status"`
		Generation   uint64       `json:"generation"`
		Config       any          `json:"config"`
		LastError    string       `json:"last_error"`
		ErrorDetail  *ErrorDetail `json:"error_detail,omitempty"`
		LastActiveAt any          `json:"last_active_at"`
		CreatedAt    any          `json:"created_at"`
	}{row.ID, row.OrgID, row.Name, row.Status, row.Generation,
		struct {
			Image     string            `json:"image"`
			Resources map[string]string `json:"resources"`
			TTL       int               `json:"ttl_seconds"`
			HasEnv    bool              `json:"has_env"`
		}{config.Image, config.Resources, config.TTLSeconds, len(config.Env) > 0},
		publicErrorMessage(detail), detail, row.LastActiveAt, row.CreatedAt}
}
func accepted(c *gin.Context, op store.ResourceOperation) {
	c.JSON(http.StatusAccepted, gin.H{"operation_id": op.ID, "status": op.Status, "resource_id": op.ResourceID})
}
func (h *Handler) List(c *gin.Context) {
	rows, err := h.svc.List(c.Request.Context(), auth.OrgID(c), auth.UID(c))
	if err != nil {
		fail(c, err)
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, View(row))
	}
	c.JSON(200, gin.H{"items": items})
}
func (h *Handler) Create(c *gin.Context) {
	var in CreateInput
	if err := bind(c, &in); err != nil {
		fail(c, ErrInvalid)
		return
	}
	op, err := h.svc.Create(c.Request.Context(), auth.OrgID(c), auth.UID(c), c.GetHeader("Idempotency-Key"), in)
	if err != nil {
		fail(c, err)
		return
	}
	accepted(c, op)
}
func (h *Handler) Get(c *gin.Context) {
	row, err := h.svc.GetOwned(c.Request.Context(), "", auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, View(row))
}
func (h *Handler) Patch(c *gin.Context) {
	var in PatchInput
	if err := bind(c, &in); err != nil {
		fail(c, ErrInvalid)
		return
	}
	if err := h.svc.Rename(c.Request.Context(), "", auth.UID(c), c.Param("id"), in.Name); err != nil {
		fail(c, err)
		return
	}
	h.Get(c)
}
func (h *Handler) Action(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in RebuildInput
		if action == "rebuild" && c.Request.ContentLength != 0 {
			if err := bind(c, &in); err != nil {
				fail(c, ErrInvalid)
				return
			}
		}
		op, err := h.svc.Action(c.Request.Context(), "", auth.UID(c), c.Param("id"), action, c.GetHeader("Idempotency-Key"), in)
		if err != nil {
			fail(c, err)
			return
		}
		accepted(c, op)
	}
}
func (h *Handler) Runs(c *gin.Context) {
	rows, err := h.svc.Runs(c.Request.Context(), "", auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	items := make([]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, gin.H{"id": r.ID, "session_id": r.SessionID, "agent_id": r.AgentID, "parent_run_id": r.ParentRunID, "status": r.Status, "lease_until": r.LeaseUntil})
	}
	c.JSON(200, gin.H{"items": items})
}
func (h *Handler) Recover(c *gin.Context) {
	var in RecoverInput
	if err := bind(c, &in); err != nil {
		fail(c, err)
		return
	}
	op, err := h.svc.Recover(c.Request.Context(), "", auth.UID(c), c.Param("id"), c.GetHeader("Idempotency-Key"), in)
	if err != nil {
		fail(c, err)
		return
	}
	accepted(c, op)
}
func (h *Handler) Bindings(c *gin.Context) {
	rows, err := h.svc.Bindings(c.Request.Context(), auth.OrgID(c), auth.UID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"items": rows})
}
func (h *Handler) Bind(c *gin.Context) {
	var in struct {
		ComputerID string `json:"computer_id"`
	}
	if err := bind(c, &in); err != nil || in.ComputerID == "" {
		fail(c, ErrInvalid)
		return
	}
	if err := h.svc.Bind(c.Request.Context(), auth.OrgID(c), auth.UID(c), c.Param("agentId"), in.ComputerID); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"computer_id": in.ComputerID})
}
func (h *Handler) Unbind(c *gin.Context) {
	if err := h.svc.Unbind(c.Request.Context(), auth.OrgID(c), auth.UID(c), c.Param("agentId")); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (h *Handler) Volumes(c *gin.Context) {
	rows, err := h.svc.Volumes(c.Request.Context(), auth.OrgID(c), auth.UID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"items": rows})
}
func (h *Handler) Operation(c *gin.Context) {
	op, err := h.svc.Operation(c.Request.Context(), auth.UID(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	detail := publicFailure(op.Error)
	if detail != nil {
		detail.OperationID = op.ID
	}
	c.JSON(200, gin.H{"id": op.ID, "resource_id": op.ResourceID, "resource_type": op.ResourceType, "action": op.Action, "status": op.Status, "error": publicErrorMessage(detail), "error_detail": detail, "created_at": op.CreatedAt, "updated_at": op.UpdatedAt})
}
