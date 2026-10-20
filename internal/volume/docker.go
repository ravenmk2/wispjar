package volume

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Docker is a deliberately narrow Engine HTTP adapter. The unversioned
// volumes endpoints are supported by Docker Engine; sockets never enter a VM.
type Docker struct {
	base   string
	client *http.Client
}

func NewDocker(endpoint string) (*Docker, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("%w: docker endpoint is not configured", ErrUnavailable)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("docker endpoint: %w", err)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid docker endpoint")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	switch u.Scheme {
	case "unix":
		if u.Path == "" || u.Host != "" {
			return nil, fmt.Errorf("invalid docker Unix endpoint")
		}
		socket := u.Path
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}
		u, _ = url.Parse("http://docker")
	case "tcp", "http", "https":
		if u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("invalid docker network endpoint")
		}
		if u.Scheme == "tcp" {
			u.Scheme = "http"
		}
	default:
		return nil, fmt.Errorf("unsupported docker endpoint scheme %q", u.Scheme)
	}
	return &Docker{base: strings.TrimRight(u.String(), "/"), client: &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (d *Docker) request(ctx context.Context, method, path string, body any) (Info, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return Info{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, bytes.NewReader(data))
	if err != nil {
		return Info{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		// Transport errors can contain private host addresses or socket paths.
		// Preserve cancellation identity without persisting daemon endpoints in
		// user-visible resource last_error fields.
		if errors.Is(err, context.Canceled) {
			return Info{}, errors.Join(ErrUnavailable, context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return Info{}, errors.Join(ErrUnavailable, context.DeadlineExceeded)
		}
		return Info{}, fmt.Errorf("%w: Docker transport request failed", ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return Info{}, ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Do not expose daemon response bodies: they can contain host paths.
		return Info{}, fmt.Errorf("%w: Docker HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	var info Info
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return Info{}, fmt.Errorf("decode Docker volume: %w", err)
	}
	return info, nil
}

func (d *Docker) Inspect(ctx context.Context, name string) (Info, error) {
	return d.request(ctx, http.MethodGet, "/volumes/"+url.PathEscape(name), nil)
}

func (d *Docker) Ensure(ctx context.Context, spec Spec) (Info, error) {
	info, err := d.Inspect(ctx, spec.Name)
	if err == nil {
		return info, checkIdentity(info, spec)
	}
	if !errors.Is(err, ErrNotFound) {
		return Info{}, err
	}
	info, err = d.request(ctx, http.MethodPost, "/volumes/create", struct {
		Name   string
		Labels map[string]string
	}{spec.Name, spec.Labels})
	if err != nil {
		return Info{}, err
	}
	return info, checkIdentity(info, spec)
}
