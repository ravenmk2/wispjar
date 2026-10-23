package sandbox

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"github.com/ravenmk2/wispjar/internal/volume"
)

var ErrComputerIdentity = errors.New("sandbox/computer_identity")
var ErrComputerMissing = errors.New("sandbox/computer_missing")
var ErrComputerAmbiguous = errors.New("sandbox/computer_ambiguous")
var ErrComputerInsecure = errors.New("computer/rebuild_required")

const computerExecdAuth = "hmac-v1"

type ComputerSpec struct {
	Installation, ComputerID, OrgID, UserID, BackendID string
	Generation                                         uint64
	SandboxID                                          *string
	Options                                            Options
	Mounts                                             []volume.Mount
}

func (s ComputerSpec) metadata() map[string]string {
	return map[string]string{"wispjar": "computer-" + s.Installation,
		"wispjar-installation": s.Installation, "wispjar-computer": s.ComputerID,
		"wispjar-generation": strconv.FormatUint(s.Generation, 10),
		"wispjar-org":        s.OrgID, "wispjar-user": s.UserID, "wispjar-backend": s.BackendID}
}

func (s ComputerSpec) matches(info opensandbox.SandboxInfo) bool {
	for k, v := range s.metadata() {
		if info.Metadata[k] != v {
			return false
		}
	}
	return true
}

// ComputerBackend implements instance lifecycle and identity checks for computers.
type ComputerBackend struct{ client *Client }

func NewComputerBackend(client *Client) *ComputerBackend { return &ComputerBackend{client: client} }

func (b *ComputerBackend) find(ctx context.Context, s ComputerSpec) (*opensandbox.SandboxInfo, error) {
	if s.SandboxID != nil {
		info, err := b.client.getSandbox(ctx, *s.SandboxID)
		if isNotFound(err) {
			return nil, ErrComputerMissing
		}
		if err != nil {
			return nil, err
		}
		if !s.matches(*info) {
			return nil, ErrComputerIdentity
		}
		return info, nil
	}
	var found *opensandbox.SandboxInfo
	for page := 1; ; page++ {
		resp, err := b.client.lifecycle.ListSandboxes(ctx, opensandbox.ListOptions{Metadata: s.metadata(), Page: page, PageSize: 100})
		if err != nil {
			return nil, err
		}
		for _, info := range resp.Items {
			if !s.matches(info) {
				return nil, ErrComputerIdentity
			}
			if found != nil {
				return nil, ErrComputerAmbiguous
			}
			copy := info
			found = &copy
		}
		if !resp.Pagination.HasNextPage {
			break
		}
	}
	if found == nil {
		return nil, ErrComputerMissing
	}
	return found, nil
}

func (b *ComputerBackend) Inspect(ctx context.Context, s ComputerSpec) (string, error) {
	info, err := b.find(ctx, s)
	if err != nil {
		return "", err
	}
	if info.Metadata["wispjar-execd-auth"] != computerExecdAuth || info.Metadata["wispjar-task-env"] != "1" {
		return "", ErrComputerInsecure
	}
	switch info.Status.State {
	case opensandbox.StateRunning:
		return "running", nil
	case opensandbox.StatePaused:
		return "paused", nil
	case opensandbox.StateTerminated, opensandbox.StateFailed:
		return "failed", nil
	default:
		return "starting", nil
	}
}

func computerVolumes(s ComputerSpec) ([]opensandbox.Volume, error) {
	if len(s.Mounts) != 3 {
		return nil, fmt.Errorf("three persistent mounts required")
	}
	seen := map[string]bool{}
	out := make([]opensandbox.Volume, 0, 3)
	for _, m := range s.Mounts {
		if (m.Name != "storage" && m.Name != "agents" && m.Name != "sessions") || seen[m.Name] || m.MountPath != "/"+m.Name || m.ClaimName == "" || m.BackendID != s.BackendID {
			return nil, fmt.Errorf("invalid persistent mount plan")
		}
		seen[m.Name] = true
		no := false
		out = append(out, opensandbox.Volume{Name: m.Name, MountPath: m.MountPath, PVC: &opensandbox.PVC{ClaimName: m.ClaimName, CreateIfNotExists: &no, DeleteOnSandboxTermination: &no}})
	}
	return out, nil
}

func computerCreateOptions(s ComputerSpec) (opensandbox.SandboxCreateOptions, error) {
	mounts, err := computerVolumes(s)
	if err != nil {
		return opensandbox.SandboxCreateOptions{}, err
	}
	ttl := s.Options.TTLSeconds
	metadata := s.metadata()
	metadata["wispjar-execd-auth"] = computerExecdAuth
	metadata["wispjar-task-env"] = "1"
	opts := opensandbox.SandboxCreateOptions{Image: s.Options.Image, Metadata: metadata, TimeoutSeconds: &ttl,
		// User environment belongs only to the non-root task. It must never
		// affect the root execd process or its maintenance shell.
		Env: map[string]string{"WISPJAR": "1"}, ResourceLimits: opensandbox.ResourceLimits(s.Options.ResourceLimits), Volumes: mounts}
	if len(s.Options.NetworkPolicy) > 0 {
		opts.NetworkPolicy, err = parseNetworkPolicy(s.Options.NetworkPolicy)
	}
	return opts, err
}

// Execd's own access token protects even loopback requests from task processes.
// The lifecycle secureAccess option only protects the Kubernetes ingress and
// is unavailable on Docker. A domain-separated token can be reconstructed on
// restart without persisting another plaintext secret in a computer record.
func (b *ComputerBackend) connection(s ComputerSpec) (opensandbox.ConnectionConfig, string, error) {
	conn := b.client.conn
	key := conn.GetAPIKey()
	if key == "" {
		return conn, "", fmt.Errorf("computer execd authentication requires a lifecycle API key")
	}
	mac := hmac.New(sha256.New, []byte(key))
	for _, value := range []string{"wispjar-execd-v1", s.Installation, s.ComputerID, strconv.FormatUint(s.Generation, 10)} {
		mac.Write([]byte(value))
		mac.Write([]byte{0})
	}
	token := hex.EncodeToString(mac.Sum(nil))
	conn.Headers = make(map[string]string, len(b.client.conn.Headers)+1)
	for k, v := range b.client.conn.Headers {
		conn.Headers[k] = v
	}
	conn.Headers["X-EXECD-ACCESS-TOKEN"] = token
	if conn.UseServerProxy {
		origin, err := url.Parse(conn.GetBaseURL())
		if err != nil {
			return conn, "", err
		}
		// SDK v1.1.0 supplies the lifecycle key as the proxy execd token,
		// overriding config headers. Replace that fallback only on this
		// lifecycle origin's exact execd proxy route, after SDK headers merge.
		client := http.Client{}
		if conn.HTTPClient != nil {
			client = *conn.HTTPClient
		}
		base := client.Transport
		if base == nil {
			if conn.Transport != nil {
				base = conn.Transport.NewTransport()
			} else {
				base = http.DefaultTransport
			}
		}
		client.Transport = computerTokenTransport{base: base, origin: origin, token: token}
		conn.HTTPClient = &client
	}
	return conn, token, nil
}

var execdProxyPath = regexp.MustCompile(`^/v1/sandboxes/[^/]+/proxy/44772(/|$)`)

type computerTokenTransport struct {
	base   http.RoundTripper
	origin *url.URL
	token  string
}

func (t computerTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == t.origin.Scheme && req.URL.Host == t.origin.Host && execdProxyPath.MatchString(req.URL.Path) {
		req = req.Clone(req.Context())
		req.Header.Set("X-EXECD-ACCESS-TOKEN", t.token)
	}
	return t.base.RoundTrip(req)
}

func (b *ComputerBackend) Ensure(ctx context.Context, s ComputerSpec, allowCreate bool) (*Handle, error) {
	conn, token, err := b.connection(s)
	if err != nil {
		return nil, err
	}
	info, err := b.find(ctx, s)
	var sb *opensandbox.Sandbox
	if errors.Is(err, ErrComputerMissing) && allowCreate && s.SandboxID == nil {
		opts, e := computerCreateOptions(s)
		if e != nil {
			return nil, e
		}
		opts.Env["EXECD_ACCESS_TOKEN"] = token
		// Never retry Create blindly: metadata discovery recovers ambiguous results.
		sb, err = opensandbox.CreateSandbox(ctx, conn, opts)
	} else if err == nil {
		if info.Metadata["wispjar-execd-auth"] != computerExecdAuth || info.Metadata["wispjar-task-env"] != "1" {
			return nil, ErrComputerInsecure
		}
		if info.Status.State == opensandbox.StatePaused {
			if err = b.client.resumeSandbox(ctx, info.ID); err != nil {
				return nil, err
			}
		}
		if info.Status.State == opensandbox.StateTerminated || info.Status.State == opensandbox.StateFailed {
			return nil, ErrComputerMissing
		}
		sb, err = opensandbox.ConnectSandbox(ctx, conn, info.ID)
	}
	if err != nil {
		return nil, err
	}
	u, err := b.client.execdURL(ctx, sb)
	if err != nil {
		return nil, err
	}
	h := &Handle{SandboxID: sb.ID(), ExecdURL: u, sandbox: sb}
	endpoint, err := sb.GetEndpoint(ctx, opensandbox.DefaultExecdPort)
	if err != nil {
		return nil, err
	}
	h.downloadClient = fileStreamClient(conn, u, token, endpoint.Headers)
	return h, nil
}

// Download bodies can exceed the SDK's ordinary request timeout. Use a separate
// client with the same transport/auth routing; the download job context bounds
// its lifetime. Never mutate a shared SDK client while other calls use it.
func fileStreamClient(conn opensandbox.ConnectionConfig, endpointURL, token string, endpointHeaders map[string]string) *opensandbox.ExecdClient {
	client := http.Client{}
	if conn.HTTPClient != nil {
		client = *conn.HTTPClient
	} else if conn.Transport != nil {
		client.Transport = conn.Transport.NewTransport()
	}
	client.Timeout = 0
	headers := make(map[string]string, len(conn.Headers)+len(endpointHeaders)+1)
	for k, v := range conn.Headers {
		headers[k] = v
	}
	for k, v := range endpointHeaders {
		headers[k] = v
	}
	headers["X-EXECD-ACCESS-TOKEN"] = token
	return opensandbox.NewExecdClient(endpointURL, token, opensandbox.WithHTTPClient(&client), opensandbox.WithHeaders(headers), opensandbox.WithTimeout(0))
}

func (b *ComputerBackend) Pause(ctx context.Context, s ComputerSpec) error {
	info, err := b.find(ctx, s)
	if err != nil {
		return err
	}
	if info.Status.State == opensandbox.StatePaused {
		return nil
	}
	return b.client.pauseSandbox(ctx, info.ID)
}
func (b *ComputerBackend) Delete(ctx context.Context, s ComputerSpec) error {
	info, err := b.find(ctx, s)
	if errors.Is(err, ErrComputerMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	err = b.client.deleteSandbox(ctx, info.ID)
	if isNotFound(err) {
		return nil
	}
	return err
}
func (b *ComputerBackend) Renew(ctx context.Context, s ComputerSpec) error {
	info, err := b.find(ctx, s)
	if err != nil {
		return err
	}
	_, err = b.client.lifecycle.RenewExpiration(ctx, info.ID, time.Now().Add(time.Duration(s.Options.TTLSeconds)*time.Second))
	return err
}
