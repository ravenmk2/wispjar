package opensandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/ravenmk2/wispjar/internal/sandbox"
)

func TestInstanceContractAndMaintenanceOwnership(t *testing.T) {
	id := "instance"
	spec := ComputerSpec{Installation: "test", ComputerID: "computer", OrgID: "org", UserID: "user", Generation: 7, SandboxID: &id}
	meta := metadata(spec)
	meta["wispjar-execd-auth"] = computerExecdAuth
	meta["wispjar-task-env"] = "1"
	state := "Running"
	resumes := 0
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/endpoints/44772"):
			json.NewEncoder(w).Encode(map[string]any{"endpoint": endpoint})
		case r.URL.Path == "/v1/sandboxes/instance/resume":
			resumes++
			state = "Running"
			w.WriteHeader(204)
		case r.URL.Path == "/v1/sandboxes/instance":
			json.NewEncoder(w).Encode(map[string]any{"id": id, "metadata": meta, "status": map[string]any{"state": state}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	endpoint = server.URL
	backend := NewComputerBackend(NewClient(server.URL, "secret", false))
	i, err := backend.Ensure(context.Background(), spec, sandbox.EnsurePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	want := sandbox.InstanceRef{InstanceID: id, ComputerID: "computer", Generation: 7}
	if i.Ref() != want {
		t.Fatalf("identity=%+v", i.Ref())
	}
	if _, ok := i.(sandbox.Operations); ok {
		t.Fatal("ordinary instance exposes root operations")
	}
	if _, err = i.Files(); err != nil {
		t.Fatal(err)
	}
	if _, err = backend.Maintainer(i); err != nil {
		t.Fatal(err)
	}
	other := NewComputerBackend(NewClient(server.URL, "secret", false))
	if _, err = other.Maintainer(i); !errors.Is(err, sandbox.ErrComputerIdentity) {
		t.Fatalf("foreign handle: %v", err)
	}
	if _, err = backend.Maintainer(nil); !errors.Is(err, sandbox.ErrComputerIdentity) {
		t.Fatalf("nil handle: %v", err)
	}
	state = "Paused"
	if _, err = backend.Ensure(context.Background(), spec, sandbox.EnsurePolicy{}); !errors.Is(err, sandbox.ErrConflict) || resumes != 0 {
		t.Fatalf("implicit resume: %d %v", resumes, err)
	}
	if _, err = backend.Ensure(context.Background(), spec, sandbox.EnsurePolicy{AllowResume: true}); err != nil || resumes != 1 {
		t.Fatalf("explicit resume: %d %v", resumes, err)
	}
	meta["wispjar-user"] = "someone-else"
	if _, err = backend.Ensure(context.Background(), spec, sandbox.EnsurePolicy{AllowCreate: true, AllowResume: true}); !errors.Is(err, sandbox.ErrComputerIdentity) {
		t.Fatalf("foreign instance: %v", err)
	}
}

func TestEnsureDoesNotCreateOnUnknownOrKnownMissingInstance(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			mutations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					mutations++
					w.WriteHeader(500)
					return
				}
				if known {
					w.WriteHeader(404)
					fmt.Fprint(w, `{"code":"NOT_FOUND"}`)
				} else {
					fmt.Fprint(w, `{"items":[],"pagination":{"hasNextPage":false}}`)
				}
			}))
			defer server.Close()
			b := NewComputerBackend(NewClient(server.URL, "secret", false))
			spec := ComputerSpec{Installation: "test", ComputerID: "computer", Generation: 1}
			if known {
				id := "missing"
				spec.SandboxID = &id
			}
			// Even allow-create must not replace a known missing ID implicitly.
			_, err := b.Ensure(context.Background(), spec, sandbox.EnsurePolicy{AllowCreate: known})
			if !errors.Is(err, sandbox.ErrComputerMissing) || mutations != 0 {
				t.Fatalf("mutation=%d err=%v", mutations, err)
			}
		})
	}
}

func TestBackendErrorsAreNeutralAndPreserveCause(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
		want   error
	}{
		{403, "DENIED", sandbox.ErrBackendAuth},
		{500, "DOCKER::IMAGE_PULL_FAILED", sandbox.ErrImageUnavailable},
		{409, "CONFLICT", sandbox.ErrConflict},
		{503, "BUSY", sandbox.ErrUnavailable},
	} {
		cause := &sdk.APIError{StatusCode: tc.status, Response: sdk.ErrorResponse{Code: tc.code}}
		err := backendError(cause)
		var original *sdk.APIError
		if !errors.Is(err, tc.want) || !errors.As(err, &original) || original != cause {
			t.Fatalf("lost category/cause: %v", err)
		}
	}
}
