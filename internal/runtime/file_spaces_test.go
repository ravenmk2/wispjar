package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

type spaceResolverFixture struct {
	FileSpaceResolver
	detail *session.Detail
	err    error
	seen   FileSpace
}

func (r *spaceResolverFixture) Resolve(_ context.Context, uid string, space FileSpace) (*session.Detail, error) {
	r.seen = space
	if uid != "owner" {
		return nil, errors.New("unexpected owner")
	}
	return r.detail, r.err
}

type spaceComputerFixture struct {
	ComputerService
	err                         error
	started, acquired, finished int
	files                       sandbox.FileReader
}

func (c *spaceComputerFixture) GetOwned(_ context.Context, org, uid, id string) (store.Computer, error) {
	if org != "org" || uid != "owner" || id != "access-computer" {
		return store.Computer{}, errors.New("unexpected computer ownership")
	}
	return store.Computer{ID: id, Generation: 7}, c.err
}
func (c *spaceComputerFixture) EnsureRunning(ctx context.Context, org, uid, id string) (store.Computer, error) {
	c.started++
	return c.GetOwned(ctx, org, uid, id)
}
func (c *spaceComputerFixture) AcquireUse(_ context.Context, org, uid, id, owner string, generation uint64) (string, error) {
	if org != "org" || uid != "owner" || id != "access-computer" || owner != "file-browser" || generation != 7 {
		return "", errors.New("invalid maintenance occupant")
	}
	c.acquired++
	return "file-use", nil
}
func (c *spaceComputerFixture) Handle(context.Context, string, string, string) (sandbox.Instance, error) {
	return readOnlyInstance{files: c.files}, nil
}
func (c *spaceComputerFixture) FinishRun(context.Context, string, string, string) error {
	c.finished++
	return nil
}

func spaceHandler() (*Handler, *spaceResolverFixture, *spaceComputerFixture) {
	bound := "session-computer"
	r := &spaceResolverFixture{detail: &session.Detail{Session: store.Session{ID: "target-session", OrgID: "org", UserID: "owner", AgentID: "agent", ComputerID: &bound}}}
	c := &spaceComputerFixture{files: &nativeReaderFixture{kind: "file", data: "hello", size: 5}}
	h := NewHandler(nil, nil, nil, nil, nil, nil, "", nil)
	h.fileSpaces, h.computers = r, c
	return h, r, c
}

func TestIndependentFileAccessUsesChosenComputerAndPreservesSession(t *testing.T) {
	for scope, root := range map[string]string{"storage": "/storage", "agent": "/agents/agent/files", "workspace": "/sessions/target-session"} {
		t.Run(scope, func(t *testing.T) {
			h, resolver, computer := spaceHandler()
			access, err := h.openSpaceAccess(context.Background(), "owner", FileSpace{OrgID: "org", ComputerID: "access-computer", Scope: scope, TargetID: "target-session"})
			if err != nil {
				t.Fatal(err)
			}
			if access.root != root || *access.session.ComputerID != "access-computer" || *resolver.detail.ComputerID != "session-computer" {
				t.Fatal("access changed target binding or root")
			}
			access.close()
			if computer.started != 1 || computer.acquired != 1 || computer.finished != 1 {
				t.Fatalf("unbalanced computer use: %+v", computer)
			}
		})
	}
}

func TestIndependentFileAccessAuthorizesBeforeStarting(t *testing.T) {
	for _, failure := range []string{"space", "computer", "missing-computer", "invalid-scope"} {
		t.Run(failure, func(t *testing.T) {
			h, resolver, computer := spaceHandler()
			space := FileSpace{OrgID: "org", ComputerID: "access-computer", Scope: "storage"}
			switch failure {
			case "space":
				resolver.err = &session.Error{Status: 404}
			case "computer":
				computer.err = &session.Error{Status: 404}
			case "missing-computer":
				space.ComputerID = ""
			case "invalid-scope":
				space.Scope = "other"
			}
			if _, err := h.openSpaceAccess(context.Background(), "owner", space); err == nil {
				t.Fatal("unauthorized access succeeded")
			}
			if computer.started != 0 || computer.acquired != 0 {
				t.Fatal("unauthorized access started computer")
			}
		})
	}
}

func TestIndependentPreviewRetainsSpaceAndRechecksRevocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, resolver, computer := spaceHandler()
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("wispjar.uid", "owner") })
	r.POST("/orgs/:orgId/previews", h.CreatePreview)
	r.GET("/api/v1/previews/:token/files/*path", h.PreviewContent)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/orgs/org/previews?computer_id=access-computer&target_id=target-session", strings.NewReader(`{"scope":"workspace","root":".","entry":"index.html"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		EntryURL string `json:"entry_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", body.EntryURL, nil))
	if w.Code != 200 || w.Body.String() != "hello" {
		t.Fatalf("read via independent computer: %d %s", w.Code, w.Body.String())
	}
	started := computer.started
	resolver.err = &session.Error{Status: 404, Code: "file/space_not_found"}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", body.EntryURL, nil))
	if w.Code != 404 || computer.started != started {
		t.Fatal("revoked space still starts computer or serves preview")
	}
}

func TestIndependentDownloadOwnershipAndReauthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, resolver, computer := spaceHandler()
	file := filepath.Join(t.TempDir(), "snapshot")
	if err := os.WriteFile(file, []byte("snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	h.downloads = map[string]*fileDownload{"key": {ID: "key", uid: "owner", Status: "ready", local: file, expires: time.Now().Add(time.Hour), space: &FileSpace{OrgID: "org", ComputerID: "access-computer", Scope: "storage"}}}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("wispjar.uid", c.GetHeader("Test-UID")) })
	r.GET("/orgs/:orgId/downloads/:downloadId", h.DownloadStatus)
	r.GET("/sessions/:id/downloads/:downloadId", h.DownloadStatus)
	r.GET("/downloads/:downloadId/file", h.DownloadContent)
	for _, tc := range []struct {
		url, uid string
		status   int
	}{
		{"/orgs/org/downloads/key", "owner", 200},
		{"/orgs/other/downloads/key", "owner", 404},
		{"/orgs/org/downloads/key", "other", 404},
		{"/sessions/target-session/downloads/key", "owner", 404},
		{"/downloads/key/file", "", 200},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", tc.url, nil)
		req.Header.Set("Test-UID", tc.uid)
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.url, w.Code, w.Body.String())
		}
	}
	resolver.err = &session.Error{Status: 403, Code: "org/not_member"}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/downloads/key/file", nil))
	if w.Code != 403 || computer.started != 0 {
		t.Fatal("revoked snapshot readable or status starts computer")
	}
}

func TestPreviewRevocationSeparatesOrganizationAndSessionGrants(t *testing.T) {
	ctx := context.Background()
	for _, independent := range []bool{true, false} {
		s := NewMemoryPreviewStore()
		grant := testPreviewGrant()
		if independent {
			grant.Space = &FileSpace{OrgID: grant.OrgID}
		}
		if err := s.Create(ctx, grant); err != nil {
			t.Fatal(err)
		}
		correct, wrong := grant.SessionID, "org:"+grant.OrgID
		if independent {
			correct, wrong = wrong, correct
		}
		if err := s.Revoke(ctx, grant.ID, grant.UserID, wrong); !errors.Is(err, ErrPreviewNotFound) {
			t.Fatal("cross-endpoint revocation succeeded")
		}
		if err := s.Revoke(ctx, grant.ID, grant.UserID, correct); err != nil {
			t.Fatal(err)
		}
	}
}
