package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/store"
)

// Exercise real middleware, handlers and generated MySQL queries without an
// external database. These callbacks supply query results, not persistence.
func authHandlerFixture(t *testing.T, mustChange bool) (*gin.Engine, string, *[]string) {
	t.Helper()
	db, err := gorm.Open(mysql.New(mysql.Config{DSN: "test:test@tcp(localhost:3306)/test", SkipInitializeWithVersion: true}), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword("original123")
	if err != nil {
		t.Fatal(err)
	}
	user := store.User{ID: "self", Username: "tester", DisplayName: "Original", Status: "active", PasswordHash: hash, MustChangePassword: mustChange}
	var updates []string
	if err := db.Callback().Query().After("gorm:query").Register("fixture:user", func(tx *gorm.DB) {
		if len(tx.Statement.Vars) == 0 || tx.Statement.Vars[0] != "self" {
			t.Errorf("query must target the authenticated user: %v", tx.Statement.Vars)
		}
		if dest, ok := tx.Statement.Dest.(*store.User); ok {
			*dest = user
			tx.RowsAffected = 1
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().After("gorm:update").Register("fixture:update", func(tx *gorm.DB) {
		updates = append(updates, tx.Statement.SQL.String())
		vars := tx.Statement.Vars
		if len(vars) == 0 || vars[len(vars)-1] != "self" {
			t.Errorf("update must target the authenticated user: %v", vars)
		}
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, "test-secret", time.Hour)
	token, err := svc.sign("self")
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	authed := r.Group("/api/v1", svc.AuthRequired())
	authed.PATCH("/auth/me", svc.UpdateMe)
	authed.POST("/auth/change-password", svc.ChangePassword)
	return r, token, &updates
}

func authRequest(r http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUpdateMeLimitsAndOwnership(t *testing.T) {
	r, token, updates := authHandlerFixture(t, false)
	for _, body := range []string{`{}`, `{"display_name":null}`, `{"display_name":"  "}`, `{"display_name":2}`, `{"display_name":"` + strings.Repeat("名", 129) + `"}`} {
		w := authRequest(r, "PATCH", "/api/v1/auth/me", token, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid name: status=%d body=%s", w.Code, w.Body)
		}
	}
	if len(*updates) != 0 {
		t.Fatal("invalid requests must not write")
	}
	name := strings.Repeat("名", 128)
	w := authRequest(r, "PATCH", "/api/v1/auth/me", token, `{"display_name":"  `+name+`  ","id":"other","username":"other","status":"disabled","password_hash":"injected","must_change_password":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("valid name: status=%d body=%s", w.Code, w.Body)
	}
	var result struct {
		User userEntry `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.User.ID != "self" || result.User.Username != "tester" || result.User.DisplayName != name || result.User.MustChangePassword {
		t.Fatalf("unexpected user response: %+v", result.User)
	}
	if len(*updates) != 1 || !strings.Contains((*updates)[0], "`display_name`=?") || strings.Contains((*updates)[0], "`username`=") || strings.Contains(w.Body.String(), "password_hash") {
		t.Fatalf("profile update leaked or changed protected fields: %v", *updates)
	}
	if w := authRequest(r, "PATCH", "/api/v1/auth/me", "", `{"display_name":"name"}`); w.Code != http.StatusUnauthorized {
		t.Fatal("profile update requires authentication")
	}
}

func TestWrongPasswordCanRetryIncludingFirstLogin(t *testing.T) {
	for _, mustChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "first-login"}[mustChange], func(t *testing.T) {
			r, token, updates := authHandlerFixture(t, mustChange)
			w := authRequest(r, "POST", "/api/v1/auth/change-password", token, `{"old_password":"wrong","new_password":"changed123"}`)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"old_password":`) || len(*updates) != 0 {
				t.Fatalf("wrong password must be a field error without writes: %d %s", w.Code, w.Body)
			}
			w = authRequest(r, "POST", "/api/v1/auth/change-password", token, `{"old_password":"original123","new_password":"changed123"}`)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"token":`) || len(*updates) != 1 {
				t.Fatalf("same login must allow retry: %d %s", w.Code, w.Body)
			}
			if mustChange {
				w = authRequest(r, "PATCH", "/api/v1/auth/me", token, `{"display_name":"name"}`)
				if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "auth/password_change_required") {
					t.Fatal("profile endpoint must not bypass first-login restriction")
				}
			}
		})
	}
}
