package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/store"
	"github.com/ravenmk2/wispjar/internal/volume"
)

func TestOperationFailureClassifiesActionableCauses(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  string
	}{
		{fmt.Errorf("adopt: %w", sandbox.ErrComputerMissing), "computer/instance_missing"},
		{&net.DNSError{Err: "no such host", Name: "private.invalid"}, "computer/backend_dns"},
		{&opensandbox.APIError{StatusCode: 500, Response: opensandbox.ErrorResponse{Code: "DOCKER::SANDBOX_START_FAILED", Message: "Egress sidecar did not become ready: Name or service not known"}}, "computer/backend_dns"},
		{errors.New("Egress sidecar did not become ready within 30s"), "computer/network_not_ready"},
		{&opensandbox.APIError{StatusCode: 403}, "computer/backend_auth"},
		{&opensandbox.APIError{StatusCode: 500, Response: opensandbox.ErrorResponse{Code: "DOCKER::IMAGE_PULL_FAILED"}}, "computer/image_unavailable"},
		{fmt.Errorf("request: %w", context.DeadlineExceeded), "computer/backend_timeout"},
		{volume.ErrMissing, "volume/missing"},
		{volume.ErrConflict, "volume/conflict"},
		{sandbox.ErrComputerIdentity, "computer/identity_mismatch"},
		{sandbox.ErrComputerInsecure, "computer/rebuild_required"},
		{errors.New("unexpected failure"), "computer/backend_failed"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			detail := publicFailure(operationError(tc.cause, "operation"))
			if detail.Code != tc.code || detail.OperationID != "operation" || detail.Message == "" || detail.Suggestion == "" {
				t.Fatalf("missing public diagnostic: %+v", detail)
			}
		})
	}
}

func TestOperationFailureDoesNotExposeBackendDetails(t *testing.T) {
	secret := "http://admin:secret-token@private.invalid:1234/internal"
	stored := operationError(errors.New("Name or service not known: "+secret), "operation")
	for _, value := range []string{stored, publicFailure(stored).Message, publicFailure(stored).Suggestion} {
		if strings.Contains(value, "secret-token") || strings.Contains(value, "private.invalid") {
			t.Fatalf("raw backend error leaked: %s", value)
		}
	}
	for _, legacy := range []string{"computer backend operation failed", secret, `{"code":"untrusted","message":"` + secret + `"}`} {
		encoded, err := json.Marshal(View(store.Computer{LastError: legacy}))
		if err != nil || strings.Contains(string(encoded), "secret-token") || !strings.Contains(string(encoded), "error_detail") {
			t.Fatalf("legacy failure leaked or lost diagnostics: %s %v", encoded, err)
		}
	}
	missing := publicFailure(operationError(sandbox.ErrComputerMissing, "operation"))
	if !strings.Contains(missing.Suggestion, "重建") || strings.Contains(missing.Suggestion, "重试启动") {
		t.Fatalf("missing instance recommends an ineffective retry: %+v", missing)
	}
	if publicFailure("") != nil || publicErrorMessage(nil) != "" {
		t.Fatal("successful computer gained an error")
	}
}
