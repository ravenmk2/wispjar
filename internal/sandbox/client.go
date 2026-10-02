package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

const markerMetadataKey = "wispjar"

type Client struct {
	conn      opensandbox.ConnectionConfig
	lifecycle *opensandbox.LifecycleClient
}

func NewClient(lifecycleURL, apiKey string) *Client {
	base := strings.TrimSuffix(strings.TrimRight(lifecycleURL, "/"), "/v1")
	return &Client{
		conn: opensandbox.ConnectionConfig{
			Domain:         base,
			APIKey:         apiKey,
			RequestTimeout: 120 * time.Second,
			DisableMetrics: true,
		},
		lifecycle: opensandbox.NewLifecycleClient(base+"/v1", apiKey),
	}
}

func (c *Client) createSandbox(ctx context.Context, sessionID string, o Options) (*opensandbox.Sandbox, error) {
	if o.Image == "" {
		return nil, fmt.Errorf("sandbox: image is required")
	}
	ttl := o.TTLSeconds
	if ttl <= 0 {
		ttl = opensandbox.DefaultTimeoutSeconds
	}
	createOpts := opensandbox.SandboxCreateOptions{
		Image:          o.Image,
		TimeoutSeconds: &ttl,
		Metadata: map[string]string{
			markerMetadataKey:  "1",
			"wispjar-session":   sessionID,
			"wispjar-createdby": "wispjar-manager",
		},
		Env: map[string]string{"WISPJAR": "1"},
	}
	if len(o.ResourceLimits) > 0 {
		createOpts.ResourceLimits = opensandbox.ResourceLimits(o.ResourceLimits)
	}
	if len(o.NetworkPolicy) > 0 {
		np, err := parseNetworkPolicy(o.NetworkPolicy)
		if err != nil {
			return nil, err
		}
		createOpts.NetworkPolicy = np
	}
	sb, err := opensandbox.CreateSandbox(ctx, c.conn, createOpts)
	if err != nil {
		return nil, fmt.Errorf("sandbox: create: %w", err)
	}
	return sb, nil
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

func (c *Client) listWispJarSandboxes(ctx context.Context) ([]opensandbox.SandboxInfo, error) {
	var out []opensandbox.SandboxInfo
	page := 1
	for {
		resp, err := c.lifecycle.ListSandboxes(ctx, opensandbox.ListOptions{
			Metadata: map[string]string{markerMetadataKey: "1"},
			Page:     page,
			PageSize: 100,
		})
		if err != nil {
			return nil, fmt.Errorf("sandbox: list: %w", err)
		}
		out = append(out, resp.Items...)
		if !resp.Pagination.HasNextPage {
			return out, nil
		}
		page++
	}
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
