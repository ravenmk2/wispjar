package runtime

import (
	"context"
	"encoding/base32"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

type previewComputer struct {
	ComputerService
	generation uint64
}

func (p previewComputer) GetOwned(context.Context, string, string, string) (store.Computer, error) {
	return store.Computer{Generation: p.generation}, nil
}

func TestPreviewRejectsStaleBindingBeforeStartingComputer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, change := range []string{"generation", "computer", "agent", "organization", "membership", "path"} {
		t.Run(change, func(t *testing.T) {
			grant := testPreviewGrant()
			token := strings.Repeat("a", 26)
			grant.TokenHash = previewHash(token)
			cid := grant.ComputerID
			detail := &session.Detail{Session: store.Session{ID: grant.SessionID, UserID: grant.UserID, OrgID: grant.OrgID, AgentID: grant.AgentID, ComputerID: &cid}}
			svc := fakeSessionService{detail: detail}
			computer := previewComputer{generation: grant.Generation}
			switch change {
			case "generation":
				computer.generation++
			case "computer":
				cid = "other"
			case "agent":
				detail.AgentID = "other"
			case "organization":
				detail.OrgID = "other"
			case "membership":
				svc.err = &session.Error{Status: 403}
			}
			h := NewHandler(nil, svc, nil, nil, nil, nil, "", nil)
			h.SetComputers(computer)
			if err := h.previews.Create(context.Background(), grant); err != nil {
				t.Fatal(err)
			}
			r := gin.New()
			r.GET("/previews/:token/files/*path", h.PreviewContent)
			file := "index.html"
			want := 404
			if change == "path" {
				file = "..%2fsecret"
				want = 400
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/previews/"+token+"/files/"+file, nil))
			if w.Code != want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if _, err := h.previews.BeginRead(context.Background(), grant.TokenHash); err != nil {
				t.Fatal("reader leaked", err)
			}
		})
	}
}

func TestPreviewTokenBase32PaddingAndLowercase(t *testing.T) {
	key := make([]byte, 16)
	for i := range key {
		key[i] = byte(i * 17)
	}
	padded := base32.StdEncoding.EncodeToString(key)
	if len(padded) != 32 || !strings.HasSuffix(padded, "======") {
		t.Fatalf("unexpected default Base32 padding: %q", padded)
	}
	token := previewTokenEncoding.EncodeToString(key)
	if len(token) != 26 || token != strings.ToLower(strings.TrimSuffix(padded, "======")) || strings.Contains(token, "=") {
		t.Fatalf("unexpected lowercase, unpadded encoding: %q", token)
	}
	for range 32 {
		generated, err := newPreviewToken()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := previewTokenEncoding.DecodeString(generated)
		if err != nil || len(decoded) != 16 || !validPreviewToken(generated) || generated != strings.ToLower(generated) || strings.Contains(generated, "=") {
			t.Fatal("generator must return 16 random bytes as 26 lowercase characters without padding")
		}
	}
}

func TestPreviewOwnerRejectsNoncanonicalTokens(t *testing.T) {
	valid := strings.Repeat("a", 26)
	for _, token := range []string{valid, strings.ToUpper(valid), valid + "======", strings.Repeat("a", 25) + "0", strings.Repeat("a", 25) + "b", strings.Repeat("a", 64), strings.Repeat("a", 25) + "\n"} {
		h := NewHandler(nil, nil, nil, nil, nil, nil, "", nil)
		grant := testPreviewGrant()
		grant.TokenHash = previewHash(token)
		if err := h.previews.Create(context.Background(), grant); err != nil {
			t.Fatal(err)
		}
		owner := h.PreviewOwner(context.Background(), token)
		if token == valid && owner != grant.UserID || token != valid && owner != "" {
			t.Fatalf("unexpected authorization for token %q", token)
		}
	}
}

func TestPreviewResourceMIMEHeadAndSandbox(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"index.html", "image.svg", "style.css", "module.mjs", "font.woff2", "README.md", "data.json", "opaque.unknown"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(method, "https://localhost/api/v1/previews/token/files/"+name, nil)
			c.Params = gin.Params{{Key: "token", Value: "token"}}
			writePreviewContent(c, name, []byte("raw bytes"))
			if w.Code != 200 || w.Header().Get("Content-Type") != previewMIME(name) || w.Header().Get("Content-Length") != "9" {
				t.Fatalf("%s %s: %v", method, name, w.Header())
			}
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("HEAD returned body")
			}
			csp := w.Header().Get("Content-Security-Policy")
			if !strings.Contains(csp, "sandbox allow-scripts;") || strings.Contains(csp, "allow-same-origin") || !strings.Contains(csp, "https://localhost/api/v1/previews/token/files/") {
				t.Fatal(csp)
			}
		}
	}
}

func TestPreviewRevokeUsesManagementIDAndOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	grant := testPreviewGrant()
	h := NewHandler(nil, fakeSessionService{detail: &session.Detail{}}, nil, nil, nil, nil, "", nil)
	if err := h.previews.Create(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("wispjar.uid", c.GetHeader("Test-UID")) })
	r.DELETE("/sessions/:id/previews/:previewId", h.RevokePreview)
	for _, uid := range []string{"other", grant.UserID, grant.UserID} {
		req := httptest.NewRequest("DELETE", "/sessions/"+grant.SessionID+"/previews/"+grant.ID, nil)
		req.Header.Set("Test-UID", uid)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		_, err := h.previews.ByTokenHash(context.Background(), grant.TokenHash)
		if (uid == "other") != (err == nil) {
			t.Fatal("owner isolation or idempotence failed")
		}
	}
}
