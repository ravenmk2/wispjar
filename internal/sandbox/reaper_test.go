package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestShouldRenew(t *testing.T) {
	now := time.Now()
	ttl := time.Hour
	idleTTL := 30 * time.Minute
	cases := []struct {
		name       string
		lastActive time.Time
		expiresAt  time.Time
		paused     bool
		want       bool
	}{
		{"临近过期且活跃", now.Add(-time.Minute), now.Add(ttl/2 - time.Minute), false, true},
		{"刚过期且活跃", now.Add(-time.Minute), now.Add(-time.Second), false, true},
		{"临边界恰好TTL/2", now.Add(-time.Minute), now.Add(ttl / 2), false, true},
		{"距离过期尚早", now.Add(-time.Minute), now.Add(ttl/2 + time.Minute), false, false},
		{"临近过期但已无活动", now.Add(-idleTTL), now.Add(time.Minute), false, false},
		{"无硬TTL", now.Add(-time.Minute), time.Time{}, false, false},
		{"paused 不受 idle 限制", now.Add(-2 * idleTTL), now.Add(time.Minute), true, true},
		{"paused 距离过期尚早", now.Add(-2 * idleTTL), now.Add(ttl/2 + time.Minute), true, false},
	}
	for _, c := range cases {
		if got := shouldRenew(now, c.lastActive, c.expiresAt, ttl, idleTTL, c.paused); got != c.want {
			t.Fatalf("%s: want %v got %v", c.name, c.want, got)
		}
	}
}

type fakeRenewRecorder struct {
	mu    sync.Mutex
	calls []time.Duration
}

func (f *fakeRenewRecorder) renew(expiresIn time.Duration) func(context.Context, time.Duration) (time.Time, error) {
	return func(_ context.Context, d time.Duration) (time.Time, error) {
		f.mu.Lock()
		f.calls = append(f.calls, d)
		f.mu.Unlock()
		return time.Now().Add(expiresIn), nil
	}
}

func newTestHandle(sessionID string, ttlSeconds int, lastActive, expiresAt time.Time, rec *fakeRenewRecorder, expiresIn time.Duration) *Handle {
	h := &Handle{SandboxID: "sb-" + sessionID, sessionID: sessionID, ttlSeconds: ttlSeconds}
	h.lastActive.Store(lastActive.UnixNano())
	if !expiresAt.IsZero() {
		h.expiresAt.Store(expiresAt.UnixNano())
	}
	if rec != nil {
		h.renewFn = rec.renew(expiresIn)
	}
	return h
}

func TestRenewActiveTiming(t *testing.T) {
	now := time.Now()
	ttlSeconds := 3600
	ttl := time.Duration(ttlSeconds) * time.Second

	nearRec := &fakeRenewRecorder{}
	farRec := &fakeRenewRecorder{}
	idleRec := &fakeRenewRecorder{}

	svc := NewService(nil, 1800, nil)
	svc.handles = map[string]*Handle{
		// 距离过期 5 分钟（< TTL/2）且 1 分钟前有活动 → 续约
		"near": newTestHandle("near", ttlSeconds, now.Add(-time.Minute), now.Add(5*time.Minute), nearRec, ttl),
		// 距离过期 50 分钟（> TTL/2）→ 不续约
		"far": newTestHandle("far", ttlSeconds, now.Add(-time.Minute), now.Add(50*time.Minute), farRec, ttl),
		// 距离过期 5 分钟但已空闲超过 idle_ttl → 不续约（留给 idle 暂停）
		"idle": newTestHandle("idle", ttlSeconds, now.Add(-time.Hour), now.Add(5*time.Minute), idleRec, ttl),
	}

	svc.renewActive(context.Background())

	if got := len(nearRec.calls); got != 1 {
		t.Fatalf("near: want 1 renew call, got %d", got)
	}
	if nearRec.calls[0] != ttl {
		t.Fatalf("near: renew duration want %v, got %v", ttl, nearRec.calls[0])
	}
	if got := len(farRec.calls); got != 0 {
		t.Fatalf("far: want no renew, got %d", got)
	}
	if got := len(idleRec.calls); got != 0 {
		t.Fatalf("idle: want no renew, got %d", got)
	}

	near := svc.handles["near"]
	if remain := time.Until(near.ExpiresAt()); remain < ttl-time.Minute {
		t.Fatalf("near: expiresAt not extended, remaining %v", remain)
	}
}

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

func TestApplySettingsDefaults(t *testing.T) {
	svc := NewService(nil, 1800, StaticSettings(Settings{
		DefaultImage:   "platform:default",
		TTLSeconds:     7200,
		ResourceLimits: map[string]string{"cpu": "2"},
		EgressPolicy:   []byte(`{"defaultAction":"deny"}`),
	}))
	got := svc.applySettings(context.Background(), Options{})
	if got.Image != "platform:default" || got.TTLSeconds != 7200 ||
		got.ResourceLimits["cpu"] != "2" || string(got.NetworkPolicy) != `{"defaultAction":"deny"}` {
		t.Fatalf("defaults not applied: %+v", got)
	}
	override := Options{
		Image:          "agent:custom",
		TTLSeconds:     60,
		ResourceLimits: map[string]string{"cpu": "4"},
		NetworkPolicy:  []byte(`{"defaultAction":"allow"}`),
	}
	got2 := svc.applySettings(context.Background(), override)
	if got2.Image != override.Image || got2.TTLSeconds != override.TTLSeconds ||
		got2.ResourceLimits["cpu"] != "4" || string(got2.NetworkPolicy) != string(override.NetworkPolicy) {
		t.Fatalf("overrides must win: %+v", got2)
	}
}
