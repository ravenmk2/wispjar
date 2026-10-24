package runtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrPreviewNotFound = errors.New("preview not found or expired")
	ErrPreviewBusy     = errors.New("preview quota exceeded")
)

const (
	previewTTL       = 15 * time.Minute
	previewFileLimit = 8 << 20
	previewByteLimit = 64 << 20
)

// PreviewGrant is immutable authorization metadata. Only a SHA-256 token hash
// is persisted; the raw token is returned once in the resource URL.
type PreviewGrant struct {
	ID, TokenHash, UserID, OrgID, SessionID, ComputerID, AgentID string
	Scope, Root                                                  string
	Generation                                                   uint64
	ExpiresAt                                                    time.Time
}

// PreviewStore can be replaced by shared storage (for example Redis). All
// quota and revocation operations must be atomic across callers. Expired grants
// must never authorize access. BeginRead/FinishRead balance active readers;
// FinishRead must release the reader even when its byte budget is exceeded.
type PreviewStore interface {
	Create(context.Context, PreviewGrant) error
	ByTokenHash(context.Context, string) (PreviewGrant, error)
	Revoke(context.Context, string, string, string) error // id, userID, sessionID
	BeginRead(context.Context, string) (PreviewGrant, error)
	FinishRead(context.Context, string, int64) error
}

type memoryPreview struct {
	grant            PreviewGrant
	requests, active int
	bytes            int64
}

type memoryPreviewStore struct {
	mu     sync.Mutex
	byHash map[string]*memoryPreview
	now    func() time.Time
}

func NewMemoryPreviewStore() PreviewStore {
	return &memoryPreviewStore{byHash: make(map[string]*memoryPreview), now: time.Now}
}

func (s *memoryPreviewStore) prune() {
	for key, p := range s.byHash {
		if !s.now().Before(p.grant.ExpiresAt) {
			delete(s.byHash, key)
		}
	}
}

func (s *memoryPreviewStore) Create(ctx context.Context, grant PreviewGrant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune() // Bounded store; cleanup needs no immortal background goroutine.
	count := 0
	for _, p := range s.byHash {
		if p.grant.UserID == grant.UserID {
			count++
		}
	}
	if len(s.byHash) >= 128 || count >= 8 {
		return ErrPreviewBusy
	}
	if _, exists := s.byHash[grant.TokenHash]; exists || !s.now().Before(grant.ExpiresAt) {
		return ErrPreviewNotFound
	}
	s.byHash[grant.TokenHash] = &memoryPreview{grant: grant}
	return nil
}

func (s *memoryPreviewStore) find(hash string) (*memoryPreview, error) {
	p := s.byHash[hash]
	if p == nil || !s.now().Before(p.grant.ExpiresAt) {
		delete(s.byHash, hash)
		return nil, ErrPreviewNotFound
	}
	return p, nil
}

func (s *memoryPreviewStore) ByTokenHash(ctx context.Context, hash string) (PreviewGrant, error) {
	if err := ctx.Err(); err != nil {
		return PreviewGrant{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(hash)
	if err != nil {
		return PreviewGrant{}, err
	}
	return p.grant, nil
}

func (s *memoryPreviewStore) Revoke(ctx context.Context, id, uid, sid string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	for hash, p := range s.byHash {
		if p.grant.ID == id && p.grant.UserID == uid && p.grant.SessionID == sid {
			delete(s.byHash, hash)
			return nil
		}
	}
	return ErrPreviewNotFound
}

func (s *memoryPreviewStore) BeginRead(ctx context.Context, hash string) (PreviewGrant, error) {
	if err := ctx.Err(); err != nil {
		return PreviewGrant{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(hash)
	if err != nil {
		return PreviewGrant{}, err
	}
	if p.active >= 8 || p.requests >= 512 || p.bytes >= previewByteLimit {
		return PreviewGrant{}, ErrPreviewBusy
	}
	p.active++
	p.requests++
	return p.grant, nil
}

func (s *memoryPreviewStore) FinishRead(_ context.Context, hash string, bytes int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(hash)
	if err != nil {
		return err
	}
	if p.active > 0 {
		p.active--
	}
	if bytes < 0 || bytes > previewByteLimit-p.bytes {
		p.bytes = previewByteLimit
		return ErrPreviewBusy
	}
	p.bytes += bytes
	return nil
}
