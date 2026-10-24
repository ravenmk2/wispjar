package opensandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

type Client struct {
	conn      opensandbox.ConnectionConfig
	lifecycle *opensandbox.LifecycleClient
}

func NewClient(lifecycleURL, apiKey string, useServerProxy bool) *Client {
	base := strings.TrimSuffix(strings.TrimRight(lifecycleURL, "/"), "/v1")
	return &Client{
		conn: opensandbox.ConnectionConfig{
			Domain:         base,
			APIKey:         apiKey,
			UseServerProxy: useServerProxy,
			RequestTimeout: 120 * time.Second,
			DisableMetrics: true,
		},
		lifecycle: opensandbox.NewLifecycleClient(base+"/v1", apiKey),
	}
}

func (c *Client) execdURL(ctx context.Context, sb *opensandbox.Sandbox) (string, error) {
	ep, err := sb.GetEndpoint(ctx, opensandbox.DefaultExecdPort)
	if err != nil {
		return "", fmt.Errorf("sandbox: resolve execd endpoint: %w", err)
	}
	u := c.conn.RewriteEndpointURL(ep.Endpoint)
	if !strings.HasPrefix(u, "http") {
		u = c.conn.GetProtocol() + "://" + u
	}
	return u, nil
}

func (c *Client) deleteSandbox(ctx context.Context, id string) error {
	return c.lifecycle.DeleteSandbox(ctx, id)
}

func (c *Client) pauseSandbox(ctx context.Context, id string) error {
	return c.lifecycle.PauseSandbox(ctx, id)
}

func (c *Client) resumeSandbox(ctx context.Context, id string) error {
	return c.lifecycle.ResumeSandbox(ctx, id)
}

// isNotFound 判定 lifecycle API 的 404（沙箱已消失）。
func isNotFound(err error) bool {
	var apiErr *opensandbox.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

func (c *Client) getSandbox(ctx context.Context, id string) (*opensandbox.SandboxInfo, error) {
	return c.lifecycle.GetSandbox(ctx, id)
}

func parseNetworkPolicy(raw json.RawMessage) (*opensandbox.NetworkPolicy, error) {
	var p struct {
		DefaultAction string                    `json:"defaultAction"`
		Rules         []opensandbox.NetworkRule `json:"rules"`
		Egress        []opensandbox.NetworkRule `json:"egress"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("sandbox: parse network policy: %w", err)
	}
	rules := p.Rules
	if rules == nil {
		rules = p.Egress
	}
	return &opensandbox.NetworkPolicy{DefaultAction: p.DefaultAction, Egress: rules}, nil
}
