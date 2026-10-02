package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const checkTimeout = 5 * time.Second

type CheckResult struct {
	OK        bool      `json:"ok"`
	At        time.Time `json:"at"`
	LatencyMs int64     `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
}

func Check(ctx context.Context, baseURL, apiKey string) CheckResult {
	res := CheckResult{At: time.Now()}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	res.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		res.Error = fmt.Sprintf("上游返回状态码 %d", resp.StatusCode)
		return res
	}
	res.OK = true
	return res
}
