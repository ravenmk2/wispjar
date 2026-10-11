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
				s.pauseIdle()
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

// currentIdleTTL：platform_setting 热加载值（30s 缓存源）优先，缺失/异常回退 config 值。
func (s *Service) currentIdleTTL(ctx context.Context) time.Duration {
	if s.settings != nil {
		if st, err := s.settings.Load(ctx); err == nil && st.IdleTTLSeconds > 0 {
			return time.Duration(st.IdleTTLSeconds) * time.Second
		}
	}
	return s.idleTTL
}

func (s *Service) renewActive(ctx context.Context) {
	now := time.Now()
	idleTTL := s.currentIdleTTL(ctx)
	for sessionID, h := range s.snapshotHandles() {
		if h.expired() {
			continue // 硬 TTL 到期，交给 pauseIdle 回收
		}
		ttl := time.Duration(h.ttlSeconds) * time.Second
		if ttl <= 0 {
			ttl = opensandbox.DefaultTimeoutSeconds * time.Second
		}
		if !shouldRenew(now, h.LastActive(), h.ExpiresAt(), ttl, idleTTL, h.Paused()) {
			continue
		}
		if _, err := h.Renew(ctx); err != nil {
			if isNotFound(err) {
				log.Printf("sandbox: renew %s (session %s): gone, recycling: %v", h.SandboxID, sessionID, err)
				if rerr := s.Recycle(sessionID, "renew_not_found"); rerr != nil {
					log.Printf("sandbox: recycle %s (session %s): %v", h.SandboxID, sessionID, rerr)
				}
				continue
			}
			log.Printf("sandbox: renew %s (session %s): %v", h.SandboxID, sessionID, err)
		}
	}
}

// shouldRenew：running 句柄仍有活动（idle < idleTTL，否则留给 idle 暂停）、paused 句柄
// 不受 idle 限制（暂停期间持续续约防服务端过期清理）；两者都在距离过期不足 TTL/2 时续约。
func shouldRenew(now, lastActive, expiresAt time.Time, ttl, idleTTL time.Duration, paused bool) bool {
	if !paused && now.Sub(lastActive) >= idleTTL {
		return false
	}
	if expiresAt.IsZero() || ttl <= 0 {
		return false
	}
	return expiresAt.Sub(now) <= ttl/2
}

// pauseIdle：idle 超过 idleTTL 的 running 句柄 Pause（保留文件系统，之后按需 resume）；
// 硬 TTL（expiresAt）到期的句柄无论 running/paused 都 Recycle。
func (s *Service) pauseIdle() {
	ctx := context.Background()
	idleTTL := s.currentIdleTTL(ctx)
	for sessionID, h := range s.snapshotHandles() {
		if h.expired() {
			if err := s.Recycle(sessionID, "ttl_expired"); err != nil {
				log.Printf("sandbox: recycle expired %s (session %s): %v", h.SandboxID, sessionID, err)
			}
			continue
		}
		if h.Paused() || time.Since(h.LastActive()) <= idleTTL {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := h.Pause(pctx)
		cancel()
		if err != nil {
			if isNotFound(err) {
				if rerr := s.Recycle(sessionID, "pause_not_found"); rerr != nil {
					log.Printf("sandbox: recycle %s (session %s): %v", h.SandboxID, sessionID, rerr)
				}
				continue
			}
			log.Printf("sandbox: pause %s (session %s): %v", h.SandboxID, sessionID, err)
			continue
		}
		h.setPaused(true)
		s.emit(Event{SessionID: sessionID, SandboxID: h.SandboxID, Action: EventPaused, Reason: "idle_ttl"})
	}
}

func (s *Service) reconcileOrphans(ctx context.Context) {
	list, err := s.client.listManagedSandboxes(ctx)
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
		if sid := info.Metadata["wispjar-session"]; sid != "" && s.adoptable(ctx, sid, info) {
			if err := s.adopt(ctx, sid, info); err != nil {
				log.Printf("sandbox: adopt %s (session %s): %v", info.ID, sid, err)
			}
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

// adoptable：metadata 里的会话在本库存在且未软删，且沙箱处于可恢复状态 → adopt 而非删除。
func (s *Service) adoptable(ctx context.Context, sessionID string, info opensandbox.SandboxInfo) bool {
	s.mu.Lock()
	exists := s.sessionExists
	s.mu.Unlock()
	if exists == nil || !exists(ctx, sessionID) {
		return false
	}
	switch info.Status.State {
	case opensandbox.StateStopping, opensandbox.StateTerminated, opensandbox.StateFailed:
		return false
	}
	return true
}

// adopt 把孤儿沙箱收编为 paused 句柄：重连后暂停（不 touch 其文件系统），之后按需 resume。
func (s *Service) adopt(ctx context.Context, sessionID string, info opensandbox.SandboxInfo) error {
	connect := s.connectFn
	if connect == nil {
		connect = s.client.connectSandbox
	}
	sb, err := connect(ctx, info.ID)
	if err != nil {
		return err
	}
	h := &Handle{
		SandboxID: info.ID,
		sandbox:   sb,
		sessionID: sessionID,
	}
	if info.ExpiresAt != nil {
		h.expiresAt.Store(info.ExpiresAt.UnixNano())
	}
	h.Touch()
	if info.Status.State != opensandbox.StatePaused {
		if err := h.Pause(ctx); err != nil {
			log.Printf("sandbox: pause adopted %s (session %s): %v", info.ID, sessionID, err)
		}
	}
	h.setPaused(true)
	s.mu.Lock()
	if _, ok := s.handles[sessionID]; ok {
		s.mu.Unlock()
		s.killBestEffort(h)
		return nil
	}
	s.handles[sessionID] = h
	s.mu.Unlock()
	log.Printf("sandbox: adopted orphan %s as paused (session %s)", info.ID, sessionID)
	s.emit(Event{SessionID: sessionID, SandboxID: info.ID, Action: EventPaused, Reason: "adopted"})
	return nil
}
