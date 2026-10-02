package ws

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestUpgradeRequiresValidToken(t *testing.T) {
	env := newTestEnv(t, nil)
	for _, url := range []string{env.http.URL + "/api/ws", env.http.URL + "/api/ws?token=bad"} {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: want 401, got %d", url, resp.StatusCode)
		}
	}
}

func TestUpgradeRejectsInactiveUser(t *testing.T) {
	env := newTestEnv(t, nil)
	_, resp, err := websocket.DefaultDialer.Dial(env.wsURL+"?token="+env.token(t, "ghost"), nil)
	if err == nil {
		t.Fatal("want dial failure for inactive user")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", resp)
	}
}

func TestUpgradeOriginCheck(t *testing.T) {
	env := newTestEnv(t, func(d *Deps) {
		d.AllowedOrigins = []string{"https://allowed.example"}
	})

	_, resp, err := websocket.DefaultDialer.Dial(
		env.wsURL+"?token="+env.token(t, "u1"),
		http.Header{"Origin": []string{"https://evil.example"}},
	)
	if err == nil {
		t.Fatal("want dial failure for disallowed origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got %v", resp)
	}

	env.dial(t, "u1", http.Header{"Origin": []string{"https://allowed.example"}})

	sameHost := http.Header{"Origin": []string{"http://" + env.http.Listener.Addr().String()}}
	env.dial(t, "u1", sameHost)
}

func TestOpenUnknownEndpoint(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"nope","payload":{}}`)
	f := readFrame(t, c)
	if f.Type != "error" || f.StreamID != "s1" || f.Error == nil || f.Error.Code != "stream/not_found" {
		t.Fatalf("want stream/not_found error frame, got %+v", f)
	}
	if f := readFrame(t, c); f.Type != "end" || f.StreamID != "s1" {
		t.Fatalf("want end frame, got %+v", f)
	}
}

func TestOpenMissingFields(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1"}`)
	f := readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "request/invalid" {
		t.Fatalf("want request/invalid, got %+v", f)
	}
}

func TestOpenDuplicateStreamID(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"control","payload":{"orgId":"o1"}}`)
	if f := readFrame(t, c); f.Type != "item" {
		t.Fatalf("want baseline item, got %+v", f)
	}
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"control","payload":{"orgId":"o1"}}`)
	f := readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "stream/conflict" {
		t.Fatalf("want stream/conflict, got %+v", f)
	}
	if f := readFrame(t, c); f.Type != "end" {
		t.Fatalf("want end frame, got %+v", f)
	}
}

func TestFollowAuthzMatrix(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)

	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"session.follow","payload":{"orgId":"other","sessionId":"sess1"}}`)
	f := readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "auth/forbidden" {
		t.Fatalf("non-member: want auth/forbidden, got %+v", f)
	}
	if f := readFrame(t, c); f.Type != "end" {
		t.Fatalf("non-member: want end, got %+v", f)
	}

	writeClient(t, c, `{"type":"open","streamId":"s2","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"not-mine"}}`)
	f = readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "session/not_found" {
		t.Fatalf("non-owner: want session/not_found, got %+v", f)
	}
	if f := readFrame(t, c); f.Type != "end" {
		t.Fatalf("non-owner: want end, got %+v", f)
	}

	writeClient(t, c, `{"type":"open","streamId":"s3","endpoint":"session.follow","payload":{"sessionId":"sess1"}}`)
	f = readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "request/invalid" {
		t.Fatalf("bad payload: want request/invalid, got %+v", f)
	}
}

func TestControlAuthz(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"control","payload":{"orgId":"other"}}`)
	f := readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "auth/forbidden" {
		t.Fatalf("want auth/forbidden, got %+v", f)
	}
	if f := readFrame(t, c); f.Type != "end" {
		t.Fatalf("want end, got %+v", f)
	}
}

func TestFollowOrchestratorError(t *testing.T) {
	env := newTestEnv(t, func(d *Deps) {
		d.Orchestrator.(*fakeOrchestrator).followErr = errTest
	})
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"sess1"}}`)
	f := readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "internal" {
		t.Fatalf("want internal error frame, got %+v", f)
	}
	if f := readFrame(t, c); f.Type != "end" {
		t.Fatalf("want end, got %+v", f)
	}
}

func TestItemOnUnknownStream(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `{"type":"item","streamId":"ghost","value":{"type":"approval-response"}}`)
	f := readFrame(t, c)
	if f.Type != "error" || f.Error == nil || f.Error.Code != "stream/not_found" {
		t.Fatalf("want stream/not_found, got %+v", f)
	}
}

func TestMalformedFrameIgnored(t *testing.T) {
	env := newTestEnv(t, nil)
	c := env.dial(t, "u1", nil)
	writeClient(t, c, `not json`)
	writeClient(t, c, `{"type":"open","streamId":"s1","endpoint":"control","payload":{"orgId":"o1"}}`)
	if f := readFrame(t, c); f.Type != "item" {
		t.Fatalf("connection must survive malformed frame, got %+v", f)
	}
}

func TestHeartbeatPingAndTimeout(t *testing.T) {
	env := newTestEnv(t, func(d *Deps) {
		d.PingPeriod = 50 * time.Millisecond
		d.PongWait = 130 * time.Millisecond
	})

	responsive := env.dial(t, "u1", nil)
	pings := make(chan struct{}, 8)
	responsive.SetPingHandler(func(data string) error {
		pings <- struct{}{}
		return responsive.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	frames := make(chan testFrame, 16)
	go func() {
		for {
			_, data, err := responsive.ReadMessage()
			if err != nil {
				return
			}
			var f testFrame
			if json.Unmarshal(data, &f) == nil {
				frames <- f
			}
		}
	}()
	writeClient(t, responsive, `{"type":"open","streamId":"s1","endpoint":"session.follow","payload":{"orgId":"o1","sessionId":"sess1"}}`)
	select {
	case f := <-frames:
		if f.Type != "item" {
			t.Fatalf("want snapshot, got %+v", f)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for snapshot")
	}
	select {
	case <-pings:
	case <-time.After(time.Second):
		t.Fatal("want at least one server ping")
	}
	writeClient(t, responsive, `{"type":"item","streamId":"s1","value":{"type":"noop"}}`)
	select {
	case f := <-frames:
		if f.Type != "error" {
			t.Fatalf("responsive connection must stay alive, got %+v", f)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for uplink error frame")
	}

	silent := env.dial(t, "u1", nil)
	silent.SetPingHandler(func(string) error { return nil })
	if err := silent.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for {
		if _, _, err := silent.ReadMessage(); err != nil {
			break
		}
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("silent connection closed too early: %v", elapsed)
	}
}
