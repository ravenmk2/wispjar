package search

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDefaultUpdateRequiresAnExplicitField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.PUT("/config", NewHandler(NewService(nil, nil)).SetDefault)
	for _, body := range []string{`{}`, `null`, `{"default_provider_id":123}`, `{"default_provider_id":{}}`} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status %d", body, w.Code)
		}
	}
}
