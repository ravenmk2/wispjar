package runtime

import (
	"context"
	"errors"

	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/skill"
)

// Preparation errors cross into persistent events and REST responses. Match
// known semantic codes, but never persist a backend cause/message/URL.
func publicPreparationError(cause error) *Error {
	if errors.Is(cause, context.Canceled) {
		return &Error{Status: 409, Code: "runtime/canceled", Message: "任务已取消"}
	}
	var stopped *StoppedControlError
	if errors.As(cause, &stopped) {
		return &Error{Status: 409, Code: "workspace/initialization_failed", Message: "工作区初始化失败，请检查 inputs 和 outputs 是否为可用目录"}
	}
	code := ""
	var se *session.Error
	if errors.As(session.ComputerError(cause), &se) {
		code = se.Code
	}
	var sk *skill.Error
	if errors.As(cause, &sk) {
		code = sk.Code
	}
	for _, entry := range []Error{
		{Status: 409, Code: "computer/rebuild_required", Message: "云电脑需要重建以启用安全访问"},
		{Status: 409, Code: "computer/required", Message: "请选择或创建云电脑"},
		{Status: 404, Code: "computer/not_found", Message: "云电脑不存在或无权访问"},
		{Status: 409, Code: "computer/busy", Message: "云电脑暂不可运行，请稍后重试"},
		{Status: 503, Code: "computer/unavailable", Message: "云电脑服务暂不可用"},
		{Status: 409, Code: "workspace/migration_pending", Message: "工作区迁移尚未完成"},
		{Status: 409, Code: "skill/update_pending", Message: "技能更新正在等待已有任务结束"},
		{Status: 409, Code: "skill/sync_failed", Message: "技能同步失败，请检查安装状态"},
		{Status: 409, Code: "skill/sync_blocked", Message: "技能同步状态需要核查后恢复"},
	} {
		if entry.Code == code {
			return &entry
		}
	}
	return &Error{Status: 503, Code: "runtime/prepare_failed", Message: "运行环境准备失败，请重试或检查云电脑状态"}
}
