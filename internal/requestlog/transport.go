package requestlog

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ravenmk2/wispjar/internal/id"
)

// Identity is immutable model metadata, safe to attach to cached model clients.
type Identity struct {
	ModelID, ProviderID string
	Metadata            Metadata
}

type transport struct {
	base     http.RoundTripper
	service  *Service
	identity Identity
}

func (s *Service) Transport(base http.RoundTripper, identity Identity) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base, service: s, identity: identity}
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	s := t.service
	// Responses clients also upload files: capture only inference requests.
	path := strings.TrimRight(req.URL.Path, "/")
	if !s.enabled(t.identity.ModelID) || req.Method != http.MethodPost || (!strings.HasSuffix(path, "/chat/completions") && !strings.HasSuffix(path, "/responses")) || req.Body == nil {
		return t.base.RoundTrip(req)
	}
	n := req.ContentLength
	if n > maxPayload {
		s.droppedLarge.Add(1)
		return t.base.RoundTrip(req)
	}
	if n <= 0 {
		n = maxPayload
	}
	if !s.reserve(n) {
		s.droppedFull.Add(1)
		return t.base.RoundTrip(req)
	}
	meta := t.identity.Metadata
	meta.Method = req.Method
	meta.ContentType = req.Header.Get("Content-Type")
	// Do not store URL userinfo, query credentials, or request headers.
	safeURL := *req.URL
	safeURL.User = nil
	safeURL.RawQuery = ""
	safeURL.Fragment = ""
	meta.URL = safeURL.String()
	raw, _ := json.Marshal(meta)
	record := Record{ID: id.New(), StartedAt: time.Now().In(time.FixedZone("UTC+08:00", 8*3600)), ModelID: t.identity.ModelID, ProviderID: t.identity.ProviderID, Metadata: raw}
	record.PayloadPath = record.StartedAt.Format("2006-01-02") + "/" + record.ID + ".json"
	reader := &captureBody{body: req.Body, service: s, entry: entry{record: record, reserved: n}, expected: req.ContentLength, buffer: make([]byte, 0, int(n))}
	clone := req.Clone(req.Context())
	clone.Body = reader
	resp, err := t.base.RoundTrip(clone)
	// net/http may close request bodies asynchronously. Completion is finalized by
	// Read/Close, never by RoundTrip returning, to preserve full-duplex semantics.
	return resp, err
}

type captureBody struct {
	body     io.ReadCloser
	service  *Service
	entry    entry
	expected int64
	mu       sync.Mutex
	buffer   []byte
	done     bool
}

func (b *captureBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return n, err
	}
	if len(b.buffer)+n > cap(b.buffer) {
		b.drop(true)
		return n, err
	}
	b.buffer = append(b.buffer, p[:n]...)
	if err == io.EOF || (b.expected > 0 && int64(len(b.buffer)) == b.expected) {
		if b.expected > 0 && int64(len(b.buffer)) != b.expected {
			b.drop(false)
		} else {
			b.done = true
			b.entry.body = b.buffer
			b.buffer = nil
			b.service.enqueue(b.entry)
			// The HTTP request can outlive the worker (streaming responses). Do not
			// retain a second reference after the worker releases its memory budget.
			b.entry.body = nil
		}
	} else if err != nil {
		b.drop(false)
	}
	return n, err
}
func (b *captureBody) drop(large bool) {
	b.done = true
	b.buffer = nil
	b.service.pending.Add(-b.entry.reserved)
	if large {
		b.service.droppedLarge.Add(1)
	} else {
		b.service.droppedIncomplete.Add(1)
	}
}
func (b *captureBody) Close() error {
	err := b.body.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.done {
		b.drop(false)
	}
	return err
}
