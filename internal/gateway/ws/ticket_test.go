package ws

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTicketExpiryAndSingleUse(t *testing.T) {
	now := time.Now()
	s := newTicketStore()
	s.now = func() time.Time { return now }
	ticket, expiry, err := s.issue("u1")
	if err != nil || expiry.Sub(now) != 30*time.Second || len(ticket) != 43 {
		t.Fatalf("unexpected ticket: expiry=%v err=%v", expiry, err)
	}
	now = expiry.Add(-time.Nanosecond)
	if uid, ok := s.consume(ticket); !ok || uid != "u1" {
		t.Fatal("ticket should remain valid just before expiry")
	}
	if _, ok := s.consume(ticket); ok {
		t.Fatal("consumed ticket was reused")
	}
	ticket, expiry, _ = s.issue("u1")
	now = expiry
	if _, ok := s.consume(ticket); ok {
		t.Fatal("ticket valid at expiry boundary")
	}
}

func TestTicketConcurrentConsume(t *testing.T) {
	s := newTicketStore()
	ticket, _, _ := s.issue("u1")
	var successes atomic.Int32
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := s.consume(ticket); ok {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("want exactly one consumer, got %d", successes.Load())
	}
}

func TestTicketCapacityAndCleanup(t *testing.T) {
	now := time.Now()
	s := newTicketStore()
	s.now = func() time.Time { return now }
	for i := range maxTickets {
		if _, _, err := s.issue(fmt.Sprintf("u%d", i/maxUserTickets)); err != nil {
			t.Fatal(err)
		}
		if i == maxUserTickets-1 {
			if _, _, err := s.issue("u0"); err != errTicketCapacity {
				t.Fatal("per-user capacity not enforced")
			}
		}
	}
	if _, _, err := s.issue("another-user"); err != errTicketCapacity {
		t.Fatal("global capacity not enforced")
	}
	now = now.Add(ticketTTL)
	if _, _, err := s.issue("u0"); err != nil || len(s.entries) != 1 {
		t.Fatalf("expired entries were not reclaimed: %v", err)
	}
}

func TestTicketEndpointAndHandshake(t *testing.T) {
	env := newTestEnv(t, nil)
	issue := func(token string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, env.http.URL+"/api/v1/auth/ws-ticket", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
		return resp
	}
	for _, token := range []string{"", "bad", env.token(t, "ghost")} {
		if resp := issue(token); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", resp.StatusCode)
		}
	}
	resp := issue(env.token(t, "u1"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body struct {
		Ticket    string
		ExpiresAt time.Time
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if remaining := time.Until(body.ExpiresAt); remaining <= 0 || remaining > ticketTTL {
		t.Fatalf("unexpected expiry: %v", remaining)
	}
	url := env.wsURL + "?ticket=" + body.Ticket
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, resp, err := websocket.DefaultDialer.Dial(url, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("used ticket accepted")
	}
	// Ticket consumption does not authenticate a REST request.
	if resp := issue(body.Ticket); resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("ticket accepted as JWT")
	}
	for range maxUserTickets {
		env.ticket(t, "u1")
	}
	if resp := issue(env.token(t, "u1")); resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "30" {
		t.Fatal("expected ticket capacity error")
	}
}

func TestTicketOriginAndFailedUpgrade(t *testing.T) {
	env := newTestEnv(t, nil)
	ticket := env.ticket(t, "u1")
	url := env.wsURL + "?ticket=" + ticket
	if _, resp, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"https://evil.example"}}); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatal("expected forbidden origin")
	}
	// A rejected Origin must not consume the ticket.
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	ticket = env.ticket(t, "u1")
	resp, err := http.Get(env.http.URL + "/api/ws?ticket=" + ticket)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want failed upgrade, got %d", resp.StatusCode)
	}
	if _, ok := env.server.tickets.consume(ticket); ok {
		t.Fatal("failed upgrade restored ticket")
	}
}

func TestTicketUpgradeRechecksUserAndExpiry(t *testing.T) {
	for _, reason := range []string{"disabled", "expired", "restarted"} {
		t.Run(reason, func(t *testing.T) {
			env := newTestEnv(t, nil)
			ticket := env.ticket(t, "u1")
			switch reason {
			case "disabled":
				env.store.activeUsers["u1"] = false
			case "expired":
				env.server.tickets.now = func() time.Time { return time.Now().Add(ticketTTL) }
			case "restarted":
				env.server.tickets = newTicketStore()
			}
			if _, resp, err := websocket.DefaultDialer.Dial(env.wsURL+"?ticket="+ticket, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatal("invalid ticket/user accepted")
			}
		})
	}
}
