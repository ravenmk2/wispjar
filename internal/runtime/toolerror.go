package runtime

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// toolFailurePrefix 错误文本工具结果前缀（runtime.md §4.11 错误语义）。
const toolFailurePrefix = "Tool execution failed: "

// errorResultMiddleware 工具错误归一化：除中断类信号外，一切工具执行错误转为
// 错误文本工具结果回喂模型，Go error 不再外溢（eino ToolsNode 会把工具返回的
// error 升级为整个 run 失败，见 §4.11）。Invokable 与 EnhancedInvokable 双链覆盖
// （read_media 等 enhanced 工具不经 Invokable 链）。
// 注册在链尾（贴近执行处）：审批 middleware 在其外层，审批 interrupt/deny 不经此层。
func errorResultMiddleware() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				out, err := next(ctx, input)
				if err == nil || keepToolError(ctx, err) {
					return out, err
				}
				return &compose.ToolOutput{Result: toolFailurePrefix + err.Error()}, nil
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				out, err := next(ctx, input)
				if err == nil || keepToolError(ctx, err) {
					return out, err
				}
				return &compose.EnhancedInvokableToolOutput{Result: &schema.ToolResult{
					Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: toolFailurePrefix + err.Error()}},
				}}, nil
			}
		},
	}
}

// keepToolError 判定错误是否保持上抛（不转文本结果）：
//   - eino interrupt 信号（审批等 interrupt 机制）：中断语义，不是执行失败；
//   - 外层 ctx 已取消/超时且错误即取消类（用户停止、服务中断）：中断语义必须保留。
//
// 工具自身超时（外层 ctx 仍存活，如命令执行超时）按普通执行失败转文本结果。
func keepToolError(ctx context.Context, err error) bool {
	if _, ok := compose.IsInterruptRerunError(err); ok {
		return true
	}
	if ctx.Err() != nil &&
		(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return true
	}
	return false
}
