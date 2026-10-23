package search

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ravenmk2/wispjar/internal/store"
)

// Explicit opt-in, empty disposable schema only. Never touch application data.
func TestInstanceSelectionMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("WISPJAR_SEARCH_TEST_DSN")
	if dsn == "" {
		t.Skip("set WISPJAR_SEARCH_TEST_DSN to an empty disposable MySQL schema")
	}
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	var count int64
	if err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("refusing to modify a non-empty schema")
	}
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	var callsA, callsB atomic.Int32
	var keyB atomic.Value
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { callsA.Add(1); w.WriteHeader(401) }))
	defer serverA.Close()
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsB.Add(1)
		keyB.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer serverB.Close()
	svc := NewService(db, []byte(strings.Repeat("k", 32)))
	ctx := context.Background()
	a, err := svc.Save(ctx, "test-actor", "", Input{Name: "DeepSeek A", Type: "deepseek", BaseURL: serverA.URL, APIKey: "test-a"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Save(ctx, "test-actor", "", Input{Name: "DeepSeek B", Type: "deepseek", BaseURL: serverB.URL, APIKey: "test-b"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("instances must be independent")
	}
	if err := svc.SetDefault(ctx, "test-actor", &b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(ctx, a.ID, Request{Query: "query"}); err == nil {
		t.Fatal("explicit failure must not fall back")
	}
	if callsA.Load() != 1 || callsB.Load() != 0 {
		t.Fatal("unexpected fallback")
	}
	if _, err := svc.Search(ctx, "", Request{Query: "query"}); err != nil {
		t.Fatal(err)
	}
	if callsB.Load() != 1 || keyB.Load() != "Bearer test-b" {
		t.Fatal("default not used")
	}
	if _, err := svc.Save(ctx, "test-actor", b.ID, Input{Name: b.Name, Type: b.Type, BaseURL: b.BaseURL, APIKey: "rotated-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(ctx, "", Request{Query: "query"}); err != nil {
		t.Fatal(err)
	}
	if keyB.Load() != "Bearer rotated-key" {
		t.Fatal("key rotation did not take effect")
	}
	if _, err := svc.Save(ctx, "test-actor", b.ID, Input{Name: b.Name, Type: b.Type, BaseURL: b.BaseURL, Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(ctx, "", Request{Query: "query"}); err == nil {
		t.Fatal("disabled default must fail")
	}
	if callsB.Load() != 2 {
		t.Fatal("disabled instance was called")
	}
	if err := svc.Delete(ctx, "test-actor", b.ID); err == nil {
		t.Fatal("default reference must prevent deletion")
	}
	if err := svc.SetDefault(ctx, "test-actor", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(ctx, "", Request{Query: "query"}); err == nil {
		t.Fatal("missing default must fail")
	}
	if err := svc.Delete(ctx, "test-actor", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Search(ctx, a.ID, Request{Query: "query"}); err == nil {
		t.Fatal("deleted pinned instance must fail")
	}
	options, err := svc.Options(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(options)
	if strings.Contains(string(raw), "base_url") || strings.Contains(string(raw), "key") {
		t.Fatal("options expose credentials")
	}
	var usage []store.SearchCall
	if err := db.Find(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if len(usage) != 3 {
		t.Fatalf("upstream call ledger: %+v", usage)
	}
	testFetchInstanceSelection(t, svc)
}

func testFetchInstanceSelection(t *testing.T, svc *Service) {
	ctx := context.Background()
	cfg, err := svc.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultFetchProviderID == nil {
		t.Fatal("missing builtin default")
	}
	nativeID := *cfg.DefaultFetchProviderID
	if err := svc.SetDefault(ctx, "test-actor", &nativeID); err == nil {
		t.Fatal("native selected for search")
	}
	if err := svc.Delete(ctx, "test-actor", nativeID); err == nil {
		t.Fatal("fetch default deleted")
	}
	native, err := svc.Save(ctx, "test-actor", "", Input{Name: "Native B", Type: "go_http", TimeoutSeconds: 9})
	if err != nil {
		t.Fatal(err)
	}
	if native.TimeoutSeconds != 9 || len(native.APIKeyEnc) != 0 || native.MaskedKey != "" {
		t.Fatal("native requires no key")
	}
	var aCalls, bCalls atomic.Int32
	aServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { aCalls.Add(1); w.WriteHeader(401) }))
	defer aServer.Close()
	bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer shared-key" {
			t.Error("key not reused")
		}
		if strings.HasSuffix(r.URL.Path, "/search") {
			_, _ = w.Write([]byte(`{"results":[]}`))
		} else {
			_, _ = w.Write([]byte(`{"title":"Source","content":"Evidence"}`))
		}
	}))
	defer bServer.Close()
	a, err := svc.Save(ctx, "test-actor", "", Input{Name: "Fetch A", Type: "deepseek", BaseURL: aServer.URL, APIKey: "bad-key"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Save(ctx, "test-actor", "", Input{Name: "Shared B", Type: "deepseek", BaseURL: bServer.URL, APIKey: "shared-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetFetchDefault(ctx, "test-actor", &b.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDefault(ctx, "test-actor", &b.ID); err != nil {
		t.Fatal(err)
	}
	// Resolve only the public target locally; upstream fixture runs on localhost.
	previous := netLookup
	netLookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
	}
	defer func() { netLookup = previous }()
	in := FetchRequest{URL: "https://example.com/"}
	if _, err := svc.Fetch(ctx, a.ID, in); err == nil {
		t.Fatal("failed pinned instance succeeded")
	}
	if aCalls.Load() != 1 || bCalls.Load() != 0 {
		t.Fatal("provider fallback")
	}
	if out, err := svc.Fetch(ctx, "", in); err != nil || out.ProviderID != b.ID {
		t.Fatalf("default not used: %+v %v", out, err)
	}
	if _, err := svc.Search(ctx, "", Request{Query: "q"}); err != nil {
		t.Fatal("shared instance search failed", err)
	}
	if bCalls.Load() != 2 {
		t.Fatal("key/instance not shared")
	}
	if err := svc.SetFetchDefault(ctx, "test-actor", &nativeID); err != nil {
		t.Fatal(err)
	}
	cfg, err = svc.Config(ctx)
	if err != nil || cfg.DefaultProviderID == nil || *cfg.DefaultProviderID != b.ID {
		t.Fatal("fetch update changed search default")
	}
	if _, err := svc.Save(ctx, "test-actor", b.ID, Input{Name: b.Name, Type: b.Type, BaseURL: b.BaseURL, Status: "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Fetch(ctx, b.ID, in); err == nil {
		t.Fatal("disabled instance used")
	}
	if bCalls.Load() != 2 {
		t.Fatal("disabled call made")
	}
	var calls []store.FetchCall
	if err := svc.db.Find(&calls).Error; err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("fetch ledger %+v", calls)
	}
	if _, err := svc.Fetch(ctx, native.ID, FetchRequest{URL: "http://127.0.0.1/"}); err == nil {
		t.Fatal("private target allowed")
	}
}
