package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// 打真实 OpenSandbox 的 pause/resume 行为验证（跳过条件同 TestSandboxIntegration）：
// 创建 → exec 写文件 → pause → resume（经 Get）→ exec 读文件内容一致；
// pause 幂等；resume 不存在的沙箱报 404 可被 isNotFound 识别；paused 期间 renew 行为实测记录。
func TestSandboxPauseResumeIntegration(t *testing.T) {
	if os.Getenv("WISPJAR_SANDBOX_IT") != "1" {
		t.Skip("set WISPJAR_SANDBOX_IT=1 to run against a real OpenSandbox server")
	}
	url := os.Getenv("WISPJAR_SANDBOX_URL")
	if url == "" {
		url = "http://127.0.0.1:8090"
	}
	key := os.Getenv("WISPJAR_SANDBOX_KEY")
	if key == "" {
		key = "opensandbox-dev-key"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := NewClient(url, key)
	svc := NewService(client, 1800, nil)
	defer svc.Stop()

	opts := Options{
		Image:          "python:3.12-slim",
		TTLSeconds:     600,
		ResourceLimits: map[string]string{"cpu": "1", "memory": "1Gi"},
		NetworkPolicy:  json.RawMessage(`{"defaultAction":"allow","rules":[]}`),
	}

	h, err := svc.Get(ctx, "it-pause-session", opts)
	if err != nil {
		t.Fatalf("create sandbox: %v", err)
	}
	sandboxID := h.SandboxID
	t.Logf("created sandbox id=%s", sandboxID)
	defer func() {
		// 清理：无论成败都尽力销毁测试沙箱
		_ = svc.Recycle("it-pause-session", "test_done")
		if err := client.deleteSandbox(context.Background(), sandboxID); err != nil && !isNotFound(err) {
			t.Logf("cleanup delete %s: %v", sandboxID, err)
		}
	}()

	content := "pause-resume-roundtrip\n"
	res, err := h.RunCommand(ctx, "printf '"+content+"' > /workspace/state.txt && cat /workspace/state.txt", "/workspace", nil, 30000, nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("write state file: %+v %v", res, err)
	}

	// 模拟 idle 到期（LastActive 拨回），走 pauseIdle 暂停
	h.lastActive.Store(time.Now().Add(-time.Hour).UnixNano())
	svc.pauseIdle()
	if !h.Paused() {
		t.Fatal("handle not paused after pauseIdle")
	}
	info, err := client.getSandbox(ctx, sandboxID)
	if err != nil {
		t.Fatalf("get paused sandbox: %v", err)
	}
	t.Logf("after pauseIdle: state=%s expiresAt=%v", info.Status.State, info.ExpiresAt)
	if info.Status.State != "Paused" {
		t.Fatalf("server state want Paused, got %s", info.Status.State)
	}

	// pause 幂等：实测服务端对重复 pause 返回错误（docker 409 "already paused"），
	// 因此 wispjar 侧保证不重复 pause（pauseIdle 成功后标记 paused；adopt 跳过已 Paused）。
	if err := client.pauseSandbox(ctx, sandboxID); err != nil {
		if !strings.Contains(err.Error(), "already paused") {
			t.Fatalf("second pause: unexpected error: %v", err)
		}
		t.Logf("pause is NOT idempotent server-side (already paused): %v", err)
	} else {
		t.Log("pause is idempotent server-side")
	}

	// paused 期间 renew：实测服务端是否允许续约
	if exp, rerr := h.Renew(ctx); rerr != nil {
		t.Logf("renew on paused sandbox: NOT allowed: %v", rerr)
	} else {
		t.Logf("renew on paused sandbox: allowed, expiresAt=%s", exp)
	}

	// resume 经获取路径：同一句柄恢复，文件系统保留
	h2, err := svc.Get(ctx, "it-pause-session", opts)
	if err != nil {
		t.Fatalf("get (resume): %v", err)
	}
	if h2.SandboxID != sandboxID {
		t.Fatalf("resume should keep sandbox %s, got %s", sandboxID, h2.SandboxID)
	}
	if h2.Paused() {
		t.Fatal("handle still paused after resume")
	}
	res, err = h2.RunCommand(ctx, "cat /workspace/state.txt", "/workspace", nil, 30000, nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("read after resume: %+v %v", res, err)
	}
	if strings.TrimSpace(res.Stdout) != strings.TrimSpace(content) {
		t.Fatalf("state mismatch after resume: %q", res.Stdout)
	}
	t.Logf("state preserved across pause/resume: %q", res.Stdout)

	// resume 不存在的沙箱：404 可被 isNotFound 识别
	err = client.resumeSandbox(ctx, "sb-nonexistent-000")
	if err == nil {
		t.Fatal("resume non-existent sandbox should fail")
	}
	if !isNotFound(err) {
		t.Fatalf("resume non-existent: want 404-identifiable error, got %v", err)
	}
	t.Logf("resume non-existent correctly 404: %v", err)

	if err := svc.Recycle("it-pause-session", "test_done"); err != nil {
		t.Fatalf("recycle: %v", err)
	}
	if _, err := client.getSandbox(ctx, sandboxID); err == nil {
		t.Fatalf("sandbox %s still exists after recycle", sandboxID)
	}
}
