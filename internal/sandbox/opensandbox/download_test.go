package opensandbox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

func TestFileDownloadStreamHasIndependentLifetimeAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/download" || r.URL.Query().Get("path") != "/protected/artifact" || r.Header.Get("X-EXECD-ACCESS-TOKEN") != "execd-token" || r.Header.Get("X-Routing") != "endpoint-route" {
			t.Error("stream lost path or auth/routing headers")
		}
		_, _ = io.WriteString(w, "first-")
		w.(http.Flusher).Flush()
		time.Sleep(30 * time.Millisecond)
		_, _ = io.WriteString(w, "last")
	}))
	defer server.Close()
	shared := &http.Client{Timeout: time.Millisecond}
	client := fileStreamClient(opensandbox.ConnectionConfig{HTTPClient: shared, RequestTimeout: time.Millisecond}, server.URL, "execd-token", map[string]string{"X-Routing": "endpoint-route"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	reader, err := client.DownloadFile(ctx, "/protected/artifact", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "first-last" {
		t.Fatalf("stream cut short: %q %v", data, err)
	}
	if shared.Timeout != time.Millisecond {
		t.Fatal("shared client timeout was mutated")
	}
}
