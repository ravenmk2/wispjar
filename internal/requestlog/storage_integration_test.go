package requestlog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

func TestStorageMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("WISPJAR_REQUEST_LOG_TEST_DSN")
	if dsn == "" {
		t.Skip("set WISPJAR_REQUEST_LOG_TEST_DSN to an empty disposable MySQL schema")
	}
	db, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	var count int64
	if err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()").Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("refusing non-empty test schema")
	}
	if err = store.Migrate(db); err != nil {
		t.Fatal(err)
	}
	provider := store.ModelProvider{ID: id.New(), Name: "Test", Type: "openai_compat", BaseURL: "http://localhost", APIKeyEnc: []byte("test"), Status: "active", CreatedBy: id.New()}
	if err = db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	model := store.Model{ID: id.New(), ProviderID: provider.ID, ModelCode: "example", DisplayName: "Example", Capabilities: json.RawMessage(`{}`), DefaultParams: json.RawMessage(`{}`), Status: "active"}
	if err = db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s, err := New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := Config{Enabled: true, RetentionDays: 7, MaxStorageBytes: 1 << 20}
	if s.enabled(model.ID) {
		t.Fatal("logging enabled by default")
	}
	if err = s.SetModel(ctx, model.ID, true); err != nil {
		t.Fatal(err)
	}
	if s.enabled(model.ID) {
		t.Fatal("master switch bypassed")
	}
	if err = s.SetConfig(ctx, cfg, provider.CreatedBy); err != nil {
		t.Fatal(err)
	}
	if !s.enabled(model.ID) {
		t.Fatal("cache not updated")
	}
	wire := []byte("{\n\"input\":\"保持原始空格\", \"stream\": true\n}")
	base := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if !bytes.Equal(got, wire) {
			t.Error("wire payload changed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
	})
	req, _ := http.NewRequest(http.MethodPost, "http://localhost/responses", bytes.NewReader(wire))
	resp, err := s.Transport(base, Identity{ModelID: model.ID, ProviderID: provider.ID}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	closeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = s.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	var row Record
	if err = db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "ready" {
		t.Fatalf("state %s", row.Status)
	}
	path, _ := s.path(row.PayloadPath)
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, wire) {
		t.Fatal("disk bytes differ", err)
	}
	sum := sha256.Sum256(wire)
	if row.PayloadSHA256 != hex.EncodeToString(sum[:]) || row.PayloadBytes != int64(len(wire)) {
		t.Fatal("digest/size mismatch")
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	s.Register(router.Group(""))
	out := httptest.NewRecorder()
	router.ServeHTTP(out, httptest.NewRequest("GET", "/request-logs/"+row.ID+"/payload", nil))
	if out.Code != 200 || !bytes.Equal(out.Body.Bytes(), wire) || out.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("payload endpoint mismatch")
	}
	// Simulate a crash after atomic rename but before the database state update.
	if err = db.Model(&row).Update("status", "writing").Error; err != nil {
		t.Fatal(err)
	}
	if err = s.recoverFiles(); err != nil {
		t.Fatal(err)
	}
	if err = db.First(&row, "id = ?", row.ID).Error; err != nil || row.Status != "ready" {
		t.Fatal("recovery failed", err)
	}
	// Retry of the same entry must not duplicate rows or bytes.
	if err = s.persist(entry{record: row, body: wire}); err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&Record{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("duplicate retry", err)
	}
	// Missing files recover as failed and remain independently removable.
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&row).Update("status", "writing").Error; err != nil {
		t.Fatal(err)
	}
	if err = s.recoverFiles(); err != nil {
		t.Fatal(err)
	}
	if err = db.First(&row, "id = ?", row.ID).Error; err != nil || row.Status != "failed" {
		t.Fatal("partial file marked ready", err)
	}
	if err = s.markAndDelete("id = ?", row.ID); err != nil {
		t.Fatal(err)
	}
	// Capacity removes the oldest complete payload before adding a newer one.
	makeEntry := func(at time.Time) entry {
		key := id.New()
		return entry{record: Record{ID: key, StartedAt: at, ModelID: model.ID, ProviderID: provider.ID, PayloadPath: at.Format("2006-01-02") + "/" + key + ".json", Metadata: json.RawMessage(`{}`)}, body: bytes.Repeat([]byte("x"), 700<<10)}
	}
	older := makeEntry(time.Now().Add(-time.Hour))
	newer := makeEntry(time.Now())
	if err = s.persist(older); err != nil {
		t.Fatal(err)
	}
	if err = s.persist(newer); err != nil {
		t.Fatal(err)
	}
	oldPath, _ := s.path(older.record.PayloadPath)
	if _, err = os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("old file not evicted")
	}
	if err = db.Model(&Record{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("capacity rows", err)
	}
	// Clearing does not mutate independent model settings.
	if err = s.markAndDelete("1 = 1"); err != nil {
		t.Fatal(err)
	}
	var setting ModelSetting
	if err = db.First(&setting, "model_id = ?", model.ID).Error; err != nil || !setting.Enabled {
		t.Fatal("clear changed model setting", err)
	}
	// Restart restores both switches; normal shutdown is bounded.
	restarted, err := New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.enabled(model.ID) {
		t.Fatal("cache reload failed")
	}
	if err = restarted.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "model-request-logs")); err != nil {
		t.Fatal(err)
	}
}
