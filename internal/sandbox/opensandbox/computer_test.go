package opensandbox

import (
	"net/http"
	"net/url"
	"testing"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/ravenmk2/wispjar/internal/volume"
)

func TestComputerIdentityAndPersistentMounts(t *testing.T) {
	s := ComputerSpec{Installation: "installation", ComputerID: "computer", OrgID: "org", UserID: "user", Generation: 3}
	if metadata(s)["wispjar"] == "1" {
		t.Fatal("legacy reaper can discover computer")
	}
	info := opensandbox.SandboxInfo{Metadata: metadata(s)}
	if !matches(s, info) {
		t.Fatal("same identity rejected")
	}
	for _, key := range []string{"wispjar", "wispjar-installation", "wispjar-generation", "wispjar-computer", "wispjar-user", "wispjar-org"} {
		info.Metadata = metadata(s)
		info.Metadata[key] = "other"
		if matches(s, info) {
			t.Fatalf("ignored identity %s", key)
		}
	}
	for _, kind := range []string{"storage", "agents", "sessions"} {
		s.Mounts = append(s.Mounts, volume.Mount{Name: kind, ClaimName: "volume-" + kind, MountPath: "/" + kind})
	}
	mounts, err := computerVolumes(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range mounts {
		if v.PVC == nil || v.PVC.CreateIfNotExists == nil || *v.PVC.CreateIfNotExists || v.PVC.DeleteOnSandboxTermination == nil || *v.PVC.DeleteOnSandboxTermination {
			t.Fatal("unsafe volume lifecycle flags")
		}
	}
	s.Mounts[0].MountPath = "/other"
	if _, err = computerVolumes(s); err == nil {
		t.Fatal("incorrect mount path accepted")
	}
}

type roundTripCheck func(*http.Request) (*http.Response, error)

func (f roundTripCheck) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestComputerProxyTokenOverrideIsScoped(t *testing.T) {
	origin, _ := url.Parse("http://localhost:8090")
	for _, target := range []struct{ url, want string }{
		{"http://localhost:8090/v1/sandboxes/example/proxy/44772/command", "derived"},
		{"http://localhost:8090/v1/sandboxes/example/proxy/44772", "derived"},
		{"http://localhost:8090/v1/sandboxes/example/proxy/44773/command", "sdk-fallback"},
		{"http://localhost:8090/v1/sandboxes/example", "sdk-fallback"},
		{"http://other.invalid/v1/sandboxes/example/proxy/44772/command", "sdk-fallback"},
	} {
		req, _ := http.NewRequest(http.MethodGet, target.url, nil)
		req.Header.Set("X-EXECD-ACCESS-TOKEN", "sdk-fallback")
		transport := computerTokenTransport{origin: origin, token: "derived", base: roundTripCheck(func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("X-EXECD-ACCESS-TOKEN"); got != target.want {
				t.Fatalf("%s: token = %s", target.url, got)
			}
			return &http.Response{StatusCode: 200}, nil
		})}
		_, _ = transport.RoundTrip(req)
		if req.Header.Get("X-EXECD-ACCESS-TOKEN") != "sdk-fallback" {
			t.Fatal("shared request headers mutated")
		}
	}
}

func TestComputerExecutionEndpointAndRootEnvironment(t *testing.T) {
	s := ComputerSpec{Installation: "installation", ComputerID: "computer", OrgID: "org", UserID: "user", Generation: 1,
		Options: Options{Image: "python:3.12", TTLSeconds: 3600, Env: map[string]string{"LD_PRELOAD": "/storage/payload.so", "PATH": "/storage/bin", "USER_SECRET": "secret"}}}
	for _, kind := range []string{"storage", "agents", "sessions"} {
		s.Mounts = append(s.Mounts, volume.Mount{Name: kind, ClaimName: "volume-" + kind, MountPath: "/" + kind})
	}
	opts, err := computerCreateOptions(s)
	if err != nil {
		t.Fatal(err)
	}
	if opts.SecureAccess || opts.Metadata["wispjar-execd-auth"] != computerExecdAuth || opts.Metadata["wispjar-task-env"] != "1" {
		t.Fatal("computer can expose a privileged anonymous execution endpoint")
	}
	for _, key := range []string{"LD_PRELOAD", "PATH", "USER_SECRET"} {
		if _, ok := opts.Env[key]; ok {
			t.Fatalf("task environment %s leaked into root container environment", key)
		}
	}
}

func TestComputerExecdTokenSurvivesRestartAndSeparatesGenerations(t *testing.T) {
	spec := ComputerSpec{Installation: "installation", ComputerID: "computer", Generation: 1}
	backend := NewComputerBackend(NewClient("http://localhost", "test-server-secret", true))
	conn, token, err := backend.connection(spec)
	if err != nil || len(token) != 64 || conn.Headers["X-EXECD-ACCESS-TOKEN"] != token {
		t.Fatal("missing execution authentication")
	}
	restarted := NewComputerBackend(NewClient("http://localhost", "test-server-secret", true))
	_, same, err := restarted.connection(spec)
	if err != nil || token != same {
		t.Fatal("restart changed token")
	}
	for _, other := range []ComputerSpec{
		{Installation: "installation", ComputerID: "computer", Generation: 2},
		{Installation: "installation", ComputerID: "other", Generation: 1},
		{Installation: "other", ComputerID: "computer", Generation: 1},
	} {
		_, value, err := backend.connection(other)
		if err != nil || value == token {
			t.Fatal("token crosses resource identity")
		}
	}
	if len(backend.client.conn.Headers) != 0 {
		t.Fatal("connection mutated shared lifecycle headers")
	}
	t.Setenv("OPEN_SANDBOX_API_KEY", "")
	empty := NewComputerBackend(NewClient("http://localhost", "", false))
	// An explicit empty lifecycle credential must not yield a public HMAC key.
	empty.client.conn.APIKey = ""
	if _, _, err := empty.connection(spec); err == nil {
		t.Fatal("unauthenticated lifecycle permits derivable execution token")
	}
}
