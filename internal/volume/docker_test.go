package volume

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDockerEnsureRejectsCreateRace(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{true: "foreign", false: "owned"}[foreign], func(t *testing.T) {
			spec := Spec{Name: "test-volume", Labels: map[string]string{"wispjar.installation": "installation"}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					http.NotFound(w, r)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/volumes/create" {
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
				var got Spec
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Error(err)
				}
				if got.Name != spec.Name || got.Labels["wispjar.installation"] != "installation" {
					t.Errorf("lost request identity %+v", got)
				}
				if foreign {
					got.Labels["wispjar.installation"] = "another"
				}
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(Info{Name: got.Name, Labels: got.Labels})
			}))
			defer server.Close()
			p, err := NewDocker(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.Ensure(context.Background(), spec)
			if foreign && !errors.Is(err, ErrConflict) {
				t.Fatalf("accepted foreign volume: %v", err)
			}
			if !foreign && err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestDockerInspectFailureIsNotAbsence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("attempted creation on inspection failure")
		}
		http.Error(w, "private host diagnostics", http.StatusInternalServerError)
	}))
	defer server.Close()
	p, err := NewDocker(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Ensure(context.Background(), Spec{Name: "test"})
	if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) {
		t.Fatalf("unexpected error %v", err)
	}
}
