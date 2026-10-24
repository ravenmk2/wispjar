package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testPreviewGrant() PreviewGrant {
	return PreviewGrant{ID: "preview", TokenHash: previewHash("token"), UserID: "owner", OrgID: "org", SessionID: "session", ComputerID: "computer", AgentID: "agent", Scope: "workspace", Root: "outputs/site", Generation: 1, ExpiresAt: time.Now().Add(time.Hour)}
}

func TestPreviewStoreExpiryRevocationAndIsolation(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryPreviewStore().(*memoryPreviewStore)
	grant := testPreviewGrant()
	if err := s.Create(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ByTokenHash(ctx, "token"); !errors.Is(err, ErrPreviewNotFound) {
		t.Fatal("raw token authorized")
	}
	for _, pair := range [][2]string{{"other", grant.SessionID}, {grant.UserID, "other"}} {
		if err := s.Revoke(ctx, grant.ID, pair[0], pair[1]); !errors.Is(err, ErrPreviewNotFound) {
			t.Fatal("cross-owner revocation accepted")
		}
	}
	if _, err := s.BeginRead(ctx, grant.TokenHash); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(ctx, grant.ID, grant.UserID, grant.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRead(ctx, grant.TokenHash, 12); !errors.Is(err, ErrPreviewNotFound) {
		t.Fatal("revoked in-flight request authorized")
	}
	if err := s.Create(ctx, grant); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return grant.ExpiresAt }
	if _, err := s.ByTokenHash(ctx, grant.TokenHash); !errors.Is(err, ErrPreviewNotFound) {
		t.Fatal("expiry boundary authorized")
	}
	if len(s.byHash) != 0 {
		t.Fatal("expired grant retained")
	}
}

func TestPreviewStoreConcurrentLimitsAndBudget(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryPreviewStore()
	grant := testPreviewGrant()
	if err := s.Create(ctx, grant); err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if _, err := s.BeginRead(ctx, grant.TokenHash); err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrPreviewBusy) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 8 {
		t.Fatalf("admitted %d", admitted.Load())
	}
	for range 8 {
		if err := s.FinishRead(ctx, grant.TokenHash, previewFileLimit); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BeginRead(ctx, grant.TokenHash); !errors.Is(err, ErrPreviewBusy) {
		t.Fatal("byte budget not enforced")
	}
	if err := s.Revoke(ctx, grant.ID, grant.UserID, grant.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, grant); err != nil {
		t.Fatal(err)
	}
	for range 512 {
		if _, err := s.BeginRead(ctx, grant.TokenHash); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishRead(ctx, grant.TokenHash, 0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.BeginRead(ctx, grant.TokenHash); !errors.Is(err, ErrPreviewBusy) {
		t.Fatal("request budget not enforced")
	}
}

func TestPreviewStoreBoundedAndPrunesOnCreate(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryPreviewStore().(*memoryPreviewStore)
	grant := testPreviewGrant()
	for i := range 128 {
		g := grant
		g.ID = fmt.Sprint(i)
		g.TokenHash = previewHash(g.ID)
		g.UserID = fmt.Sprint(i / 8)
		if err := s.Create(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Create(ctx, grant); !errors.Is(err, ErrPreviewBusy) {
		t.Fatal("global capacity not enforced")
	}
	expiredAt := grant.ExpiresAt
	s.now = func() time.Time { return expiredAt }
	grant.ExpiresAt = grant.ExpiresAt.Add(time.Hour)
	if err := s.Create(ctx, grant); err != nil {
		t.Fatal(err)
	}
	if len(s.byHash) != 1 {
		t.Fatal("expired capacity not reclaimed")
	}
	for i := range 7 {
		g := grant
		g.ID = fmt.Sprint(i)
		g.TokenHash = previewHash(g.ID)
		if err := s.Create(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	grant.TokenHash = previewHash("ninth")
	if err := s.Create(ctx, grant); !errors.Is(err, ErrPreviewBusy) {
		t.Fatal("owner capacity not enforced")
	}
}
