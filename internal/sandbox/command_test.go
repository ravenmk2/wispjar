package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

func TestRunCommandReturnsAtCompletionBeforeHTTPStreamCloses(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		code          int
		wantError     bool
	}{
		{name: "success", code: 0},
		{name: "nonzero exit", failure: `{"type":"error","error":{"ename":"CommandExit","evalue":"7"}}`, code: 7},
		{name: "execution failure", failure: `{"type":"error","error":{"ename":"ExecutionError","evalue":"cannot execute"}}`, code: -1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			var endpoint string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/endpoints/44772") {
					_, _ = fmt.Fprintf(w, `{"endpoint":%q}`, endpoint)
					return
				}
				if r.URL.Path != "/command" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "{\"type\":\"stdout\",\"text\":\"first\\n\\n\"}\n\n{\"type\":\"stdout\",\"text\":\"last\"}\n\n{\"type\":\"stderr\",\"text\":\"WISPJAR_STOPPED_test\"}\n\n")
				if tc.failure != "" {
					_, _ = fmt.Fprint(w, tc.failure+"\n\n")
				}
				_, _ = fmt.Fprint(w, "{\"type\":\"execution_complete\",\"execution_time\":1}\n\n")
				w.(http.Flusher).Flush()
				// Reproduce execd keeping the response open after the terminal
				// event. Returning must not depend on HTTP EOF or a timer.
				<-release
			}))
			defer server.Close()
			defer close(release)
			endpoint = server.URL
			sb, err := opensandbox.ConnectSandbox(context.Background(), opensandbox.ConnectionConfig{Domain: server.URL, DisableMetrics: true}, "test")
			if err != nil {
				t.Fatal(err)
			}
			handle := &Handle{sandbox: sb}
			type response struct {
				result *CommandResult
				err    error
				chunks []string
			}
			done := make(chan response, 1)
			go func() {
				var chunks []string
				result, err := handle.RunCommand(context.Background(), "true", "/", nil, 1000, func(stream, chunk string) { chunks = append(chunks, stream+":"+chunk) })
				done <- response{result, err, chunks}
			}()
			select {
			case got := <-done:
				if (got.err != nil) != tc.wantError || got.result.ExitCode != tc.code {
					t.Fatalf("exit status changed: %+v err=%v", got.result, got.err)
				}
				if got.result.Stdout != "first\n\nlast" || got.result.Stderr != "WISPJAR_STOPPED_test" || len(got.chunks) != 3 {
					t.Fatalf("terminal output lost: %+v chunks=%v", got.result, got.chunks)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("command waited for HTTP close after execution_complete")
			}
		})
	}
}

func TestRunCommandDoesNotTreatTruncatedStreamAsCompletion(t *testing.T) {
	var endpoint string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/endpoints/44772") {
			_, _ = fmt.Fprintf(w, `{"endpoint":%q}`, endpoint)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Length", "10000")
		_, _ = fmt.Fprint(w, "{\"type\":\"stdout\",\"text\":\"partial\"}\n\n")
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	endpoint = server.URL
	sb, err := opensandbox.ConnectSandbox(context.Background(), opensandbox.ConnectionConfig{Domain: server.URL, DisableMetrics: true}, "test")
	if err != nil {
		t.Fatal(err)
	}
	result, err := (&Handle{sandbox: sb}).RunCommand(context.Background(), "true", "/", nil, 1000, nil)
	if err == nil || result.ExitCode != -1 || result.Stdout != "partial" {
		t.Fatalf("truncated stream was reported complete: %+v %v", result, err)
	}
}
