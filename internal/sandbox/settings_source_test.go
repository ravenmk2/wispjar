package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"
)

type countingSettingsSource struct {
	mu    sync.Mutex
	calls int
	s     Settings
	err   error
}

func (c *countingSettingsSource) Load(context.Context) (Settings, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.s, c.err
}

func (c *countingSettingsSource) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestCachedSettingsSource(t *testing.T) {
	src := &countingSettingsSource{s: Settings{DefaultImage: "img:v1", TTLSeconds: 100}}
	cached := NewCachedSettingsSource(src, 40*time.Millisecond)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		st, err := cached.Load(ctx)
		if err != nil || st.DefaultImage != "img:v1" {
			t.Fatalf("load: %v %+v", err, st)
		}
	}
	if got := src.callCount(); got != 1 {
		t.Fatalf("want 1 underlying load within TTL, got %d", got)
	}

	src.mu.Lock()
	src.s.DefaultImage = "img:v2"
	src.mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	st, err := cached.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.DefaultImage != "img:v2" {
		t.Fatalf("want refreshed settings after TTL, got %q", st.DefaultImage)
	}
	if got := src.callCount(); got != 2 {
		t.Fatalf("want 2 underlying loads, got %d", got)
	}
}
