package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func invokableEndpoint(err error) compose.InvokableToolEndpoint {
	return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		return nil, err
	}
}

func enhancedEndpoint(err error) compose.EnhancedInvokableToolEndpoint {
	return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
		return nil, err
	}
}

func TestErrorResultMiddlewareConvertsError(t *testing.T) {
	mw := errorResultMiddleware()
	boom := errors.New("boom: sandbox gone")

	out, err := mw.Invokable(invokableEndpoint(boom))(context.Background(), &compose.ToolInput{})
	if err != nil {
		t.Fatalf("error must be converted, got %v", err)
	}
	if out == nil || out.Result != toolFailurePrefix+"boom: sandbox gone" {
		t.Fatalf("invokable result = %+v", out)
	}

	eout, err := mw.EnhancedInvokable(enhancedEndpoint(boom))(context.Background(), &compose.ToolInput{})
	if err != nil {
		t.Fatalf("enhanced error must be converted, got %v", err)
	}
	if eout == nil || eout.Result == nil || len(eout.Result.Parts) != 1 ||
		eout.Result.Parts[0].Type != schema.ToolPartTypeText ||
		eout.Result.Parts[0].Text != toolFailurePrefix+"boom: sandbox gone" {
		t.Fatalf("enhanced result = %+v", eout)
	}
}

func TestErrorResultMiddlewareKeepsCancel(t *testing.T) {
	mw := errorResultMiddleware()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mw.Invokable(invokableEndpoint(fmt.Errorf("dial: %w", context.Canceled)))(ctx, &compose.ToolInput{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("outer cancel must propagate, got %v", err)
	}

	dctx, dcancel := context.WithTimeout(context.Background(), 0)
	defer dcancel()
	_, err = mw.EnhancedInvokable(enhancedEndpoint(context.DeadlineExceeded))(dctx, &compose.ToolInput{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("outer deadline must propagate, got %v", err)
	}
}

func TestErrorResultMiddlewareConvertsToolInternalTimeout(t *testing.T) {
	mw := errorResultMiddleware()
	// 工具自身超时（外层 ctx 仍存活）按普通执行失败转文本结果。
	out, err := mw.Invokable(invokableEndpoint(fmt.Errorf("command timed out: %w", context.DeadlineExceeded)))(context.Background(), &compose.ToolInput{})
	if err != nil {
		t.Fatalf("tool-internal timeout must be converted, got %v", err)
	}
	if !strings.HasPrefix(out.Result, toolFailurePrefix) {
		t.Fatalf("result = %q", out.Result)
	}
}

func TestErrorResultMiddlewareKeepsInterrupt(t *testing.T) {
	mw := errorResultMiddleware()
	intr := compose.NewInterruptAndRerunErr(ApprovalInterruptInfo{CallID: "c1", Name: "exec"})
	_, err := mw.Invokable(invokableEndpoint(intr))(context.Background(), &compose.ToolInput{})
	if _, ok := compose.IsInterruptRerunError(err); !ok {
		t.Fatalf("interrupt signal must propagate untouched, got %v", err)
	}
	_, err = mw.EnhancedInvokable(enhancedEndpoint(intr))(context.Background(), &compose.ToolInput{})
	if _, ok := compose.IsInterruptRerunError(err); !ok {
		t.Fatalf("enhanced interrupt signal must propagate untouched, got %v", err)
	}
}
