package sandbox

import (
	"context"
	"log"
	"time"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

func (s *Service) Start(ctx context.Context) {
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.reconcileOrphans(ctx)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		renew := time.NewTicker(30 * time.Second)
		reap := time.NewTicker(time.Minute)
		orphan := time.NewTicker(time.Hour)
		defer renew.Stop()
		defer reap.Stop()
		defer orphan.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-renew.C:
				s.renewActive(ctx)
			case <-reap.C:
				s.reapIdle()
			case <-orphan.C:
				s.reconcileOrphans(ctx)
			}
		}
	}()
}

func (s *Service) snapshotHandles() map[string]*Handle {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*Handle, len(s.handles))
	for k, v := range s.handles {
		out[k] = v
	}
	return out
}

func (s *Service) renewActive(ctx context.Context) {
	for sessionID, h := range s.snapshotHandles() {
		idle := time.Since(h.LastActive())
		ttl := time.Duration(h.ttlSeconds) * time.Second
		if ttl <= 0 {
			ttl = opensandbox.DefaultTimeoutSeconds * time.Second
		}
		if idle >= ttl/2 {
			continue
		}
		if _, err := h.Renew(ctx); err != nil {
			log.Printf("sandbox: renew %s (session %s): %v", h.SandboxID, sessionID, err)
		}
	}
}

func (s *Service) reapIdle() {
	for sessionID, h := range s.snapshotHandles() {
		if time.Since(h.LastActive()) <= s.idleTTL {
			continue
		}
		if err := s.Recycle(sessionID, "idle_ttl"); err != nil {
			log.Printf("sandbox: recycle %s (session %s): %v", h.SandboxID, sessionID, err)
		}
	}
}

func (s *Service) reconcileOrphans(ctx context.Context) {
	list, err := s.client.listWispJarSandboxes(ctx)
	if err != nil {
		log.Printf("sandbox: orphan reconcile list: %v", err)
		return
	}
	known := make(map[string]string, len(list))
	for _, h := range s.snapshotHandles() {
		known[h.SandboxID] = ""
	}
	for _, info := range list {
		if _, ok := known[info.ID]; ok {
			continue
		}
		if time.Since(info.CreatedAt) < 2*time.Minute {
			continue
		}
		if err := s.client.deleteSandbox(ctx, info.ID); err != nil {
			log.Printf("sandbox: delete orphan %s: %v", info.ID, err)
			continue
		}
		log.Printf("sandbox: deleted orphan sandbox %s", info.ID)
		s.emit(Event{
			SessionID: info.Metadata["wispjar-session"],
			SandboxID: info.ID,
			Action:    EventRecycled,
			Reason:    "orphan",
		})
	}
}
