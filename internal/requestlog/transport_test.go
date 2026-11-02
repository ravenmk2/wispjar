package requestlog

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func testService(enabled bool) *Service {
	s := &Service{queue: make(chan entry, 2), done: make(chan struct{})}
	s.config.Store(&snapshot{Config: Config{Enabled: enabled, RetentionDays: 7, MaxStorageBytes: 5 << 30}, Models: map[string]bool{"model": true}})
	return s
}
func TestTransportCapturesExactPayloadWithoutWorker(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		t.Run(path, func(t *testing.T) {
			want := []byte("{\n \"messages\": [{\"content\":\"你好 <>&\"}], \"stream\": true }")
			received := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				received <- data
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			s := testService(true)
			client := &http.Client{Transport: s.Transport(nil, Identity{ModelID: "model"})}
			req, _ := http.NewRequest(http.MethodPost, server.URL+path+"?api_key=secret", bytes.NewReader(want))
			req.Header.Set("Authorization", "Bearer secret")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if !bytes.Equal(<-received, want) {
				t.Fatal("capture changed wire bytes")
			}
			select {
			case e := <-s.queue:
				if !bytes.Equal(e.body, want) {
					t.Fatalf("payload differs: %q", e.body)
				}
				if strings.Contains(string(e.record.Metadata), "secret") {
					t.Fatal("credentials captured")
				}
				if !strings.HasSuffix(e.record.PayloadPath, e.record.ID+".json") {
					t.Fatal("file identity mismatch")
				}
				if s.pending.Load() != int64(len(want)) {
					t.Fatal("reservation not retained for worker")
				}
			case <-time.After(time.Second):
				t.Fatal("no complete payload")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDisabledAndSaturatedCapturePreservesRequests(t *testing.T) {
	for _, mode := range []string{"disabled", "model-disabled", "full", "bytes-full", "oversize", "files", "nil"} {
		t.Run(mode, func(t *testing.T) {
			s := testService(true)
			path := "/v1/responses"
			switch mode {
			case "disabled":
				s.config.Load().Enabled = false
			case "model-disabled":
				s.config.Load().Models = map[string]bool{}
			case "full":
				s.queue <- entry{}
				s.queue <- entry{}
			case "bytes-full":
				s.pending.Store(maxPending)
			case "files":
				path = "/v1/files"
			case "nil":
				s = nil
			}
			wire := []byte(`{"input":"hello"}`)
			base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				got, err := io.ReadAll(r.Body)
				_ = r.Body.Close()
				if err != nil || !bytes.Equal(got, wire) {
					t.Fatal("request was damaged")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
			})
			req, _ := http.NewRequest(http.MethodPost, "http://localhost"+path, bytes.NewReader(wire))
			if mode == "oversize" {
				req.ContentLength = maxPayload + 1
			}
			resp, err := s.Transport(base, Identity{ModelID: "model"}).RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if s != nil {
				expected := 0
				if mode == "full" {
					expected = 2
				}
				if len(s.queue) != expected {
					t.Fatal("unexpected capture")
				}
				if mode != "bytes-full" && s.pending.Load() != 0 {
					t.Fatal("memory reservation leaked")
				}
			}
		})
	}
}

func TestIncompleteAndUnknownLength(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(map[bool]string{true: "complete", false: "partial"}[complete], func(t *testing.T) {
			s := testService(true)
			base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if complete {
					_, _ = io.Copy(io.Discard, r.Body)
				} else {
					buf := make([]byte, 2)
					_, _ = r.Body.Read(buf)
				}
				_ = r.Body.Close()
				return nil, errors.New("network failed")
			})
			req, _ := http.NewRequest(http.MethodPost, "http://localhost/responses", io.NopCloser(strings.NewReader(`{"input":"hello"}`)))
			_, _ = s.Transport(base, Identity{ModelID: "model"}).RoundTrip(req)
			if complete {
				if len(s.queue) != 1 {
					t.Fatal("complete body missing")
				}
			} else if len(s.queue) != 0 || s.pending.Load() != 0 || s.droppedIncomplete.Load() != 1 {
				t.Fatal("partial body recorded or reservation leaked")
			}
		})
	}
}

func TestConcurrentCaptureAndShutdown(t *testing.T) {
	s := testService(true)
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		return nil, errors.New("offline")
	})
	transport := s.Transport(base, Identity{ModelID: "model"})
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, "http://localhost/responses", strings.NewReader("{}"))
			_, _ = transport.RoundTrip(req)
		}()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = s.Close(ctx)
	wg.Wait()
	for e := range s.queue {
		s.pending.Add(-e.reserved)
	}
	if s.pending.Load() != 0 {
		t.Fatal("reservation leaked during shutdown")
	}
}

func TestPayloadPathCannotEscapeRoot(t *testing.T) {
	s := &Service{root: t.TempDir()}
	for _, path := range []string{"../config.yaml", "/etc/passwd", `2026-11-02\..\secret`, "2026-11-02/../secret.json"} {
		if _, err := s.path(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
}

func TestCaptureReleasesReferenceAfterEnqueue(t *testing.T) {
	s := testService(true)
	s.pending.Store(2)
	b := &captureBody{body: io.NopCloser(strings.NewReader("{}")), service: s, expected: 2, buffer: make([]byte, 0, 2), entry: entry{reserved: 2}}
	if _, err := io.Copy(io.Discard, b); err != nil {
		t.Fatal(err)
	}
	if b.buffer != nil || b.entry.body != nil {
		t.Fatal("HTTP body retains captured payload after enqueue")
	}
	if got := <-s.queue; string(got.body) != "{}" {
		t.Fatal("queued payload lost")
	}
}
