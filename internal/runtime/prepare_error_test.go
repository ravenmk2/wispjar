package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
)

type preparationFailureFactory struct{ cause error }

func (f preparationFailureFactory) NewChatModel(context.Context, string, map[string]any) (model.ToolCallingChatModel, error) {
	return nil, f.cause
}

func TestPreparationErrorDoesNotPersistBackendDetails(t *testing.T) {
	env := newTestEnv(t, session.ApprovalAuto, nil)
	cause := errors.New("POST https://user:private@backend.invalid:9000/internal failed with token=secret")
	env.orch.(*orchestrator).deps.Models = preparationFailureFactory{cause: cause}
	env.prompt(t, "start", "request")
	err := env.orch.StartTurn(context.Background(), env.session.ID)
	var public *Error
	if !errors.As(err, &public) || public.Code != "runtime/prepare_failed" {
		t.Fatalf("public error: %v", err)
	}
	events := env.es.byType(session.EventTurnEnd)
	if len(events) != 1 {
		t.Fatalf("turn ends=%d", len(events))
	}
	var end session.TurnEndData
	if err := json.Unmarshal(events[0].Data, &end); err != nil {
		t.Fatal(err)
	}
	if end.ErrorCode != "runtime/prepare_failed" || end.Error == "" {
		t.Fatalf("missing stable diagnosis: %+v", end)
	}
	for _, secret := range []string{"backend.invalid", "private", "secret", "https://"} {
		if strings.Contains(end.Error, secret) || strings.Contains(public.Error(), secret) || strings.Contains(string(events[0].Data), secret) {
			t.Fatalf("backend detail leaked: %s", secret)
		}
	}
}

func TestInsecureComputerPreparationRequestsRebuild(t *testing.T) {
	err := publicPreparationError(fmt.Errorf("endpoint https://private.invalid: %w", sandbox.ErrComputerInsecure))
	if err.Status != 409 || err.Code != "computer/rebuild_required" || strings.Contains(err.Message, "private.invalid") {
		t.Fatalf("%+v", err)
	}
}
