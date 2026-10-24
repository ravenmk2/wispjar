package ws

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ravenmk2/wispjar/internal/auth"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
)

const (
	ticketTTL      = 30 * time.Second
	maxTickets     = 4096
	maxUserTickets = 32
)

var errTicketCapacity = errors.New("too many pending websocket tickets")

type ticketEntry struct {
	uid       string
	expiresAt time.Time
}

// ticketStore is process-local. Expired entries are reclaimed on issuance;
// the capacity bounds memory even when clients abandon their tickets.
type ticketStore struct {
	mu      sync.Mutex
	entries map[[32]byte]ticketEntry
	now     func() time.Time
}

func newTicketStore() *ticketStore {
	return &ticketStore{entries: make(map[[32]byte]ticketEntry), now: time.Now}
}

func (s *ticketStore) issue(uid string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	userCount := 0
	for key, entry := range s.entries {
		if !now.Before(entry.expiresAt) {
			delete(s.entries, key)
		} else if entry.uid == uid {
			userCount++
		}
	}
	if len(s.entries) >= maxTickets || userCount >= maxUserTickets {
		return "", time.Time{}, errTicketCapacity
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", time.Time{}, err
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw[:])
	expiresAt := now.Add(ticketTTL)
	s.entries[sha256.Sum256([]byte(ticket))] = ticketEntry{uid: uid, expiresAt: expiresAt}
	return ticket, expiresAt, nil
}

// consume atomically removes a ticket before the upgrade. Failed upgrades
// never restore it: clients must request a fresh ticket for every attempt.
func (s *ticketStore) consume(ticket string) (string, bool) {
	if len(ticket) != 43 {
		return "", false
	}
	key := sha256.Sum256([]byte(ticket))
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	delete(s.entries, key)
	if !ok || !s.now().Before(entry.expiresAt) {
		return "", false
	}
	return entry.uid, true
}

// IssueTicket must be registered behind AuthRequired, never on the public WS route.
func (s *Server) IssueTicket(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uid := auth.UID(c)
	if uid == "" {
		respond.Error(c, http.StatusUnauthorized, respond.CodeUnauthorized, "请先登录", nil)
		return
	}
	ticket, expiresAt, err := s.tickets.issue(uid)
	if errors.Is(err, errTicketCapacity) {
		c.Header("Retry-After", "30")
		respond.Error(c, http.StatusTooManyRequests, respond.CodeRateLimited, "待使用的连接票据过多，请稍后重试", nil)
		return
	}
	if err != nil {
		respond.Error(c, http.StatusInternalServerError, respond.CodeInternal, "无法创建连接票据", nil)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ticket": ticket, "expiresAt": expiresAt})
}
