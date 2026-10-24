package computer

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"

	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/volume"
)

// ErrorDetail contains only public diagnostics. Raw backend errors stay in logs.
type ErrorDetail struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Suggestion  string `json:"suggestion"`
	OperationID string `json:"operation_id,omitempty"`
}

type storedFailure struct {
	Code        string `json:"code"`
	OperationID string `json:"operation_id,omitempty"`
}

var failureMessages = map[string][2]string{
	"computer/instance_missing":  {"云电脑实例不存在，上次创建或启动未成功。", "请使用“编辑 / 重建”重新创建实例，已有持久文件会保留。"},
	"computer/backend_dns":       {"云电脑后端地址无法解析，无法连接运行环境。", "请联系管理员检查后端地址和 DNS；修复后，创建失败的电脑需通过“编辑 / 重建”恢复。"},
	"computer/network_not_ready": {"云电脑网络侧车未在规定时间内就绪。", "请联系管理员检查容器网络和侧车服务，修复后重建电脑。"},
	"computer/image_unavailable": {"无法获取或启动所选镜像。", "请联系管理员检查镜像是否存在、仓库权限和网络，再选择可用镜像重建。"},
	"computer/backend_auth":      {"云电脑后端认证失败。", "请联系管理员检查后端访问凭据和执行端认证配置。"},
	"computer/backend_timeout":   {"等待云电脑后端响应超时，操作结果尚未确认。", "请稍后刷新；若持续失败，请联系管理员核对实例状态，避免重复创建。"},
	"computer/identity_mismatch": {"云电脑实例归属或代次校验未通过。", "请联系管理员核对实例状态，不要继续执行任务或手动删除数据卷。"},
	"computer/rebuild_required":  {"这台电脑缺少当前要求的执行端认证。", "请通过“编辑 / 重建”启用认证，已有持久文件会保留。"},
	"volume/missing":             {"云电脑的持久存储卷缺失。", "请联系管理员恢复原有存储卷，不要新建空卷覆盖已有数据。"},
	"volume/conflict":            {"持久存储卷的归属或配置存在冲突。", "请联系管理员核对存储卷归属和后端配置。"},
	"computer/recovery_pending":  {"尚未确认旧实例停止或遗留占用清理完成。", "请等待恢复完成；若持续未完成，请联系管理员按操作编号排查。"},
	"computer/backend_failed":    {"云电脑后端操作失败，暂未确定具体原因。", "请联系管理员，提供下方操作编号以查询详细日志。"},
	"computer/legacy_failure":    {"这条旧失败记录未保存具体原因。", "请联系管理员结合失败时间查询后端日志。"},
}

func operationError(cause error, operationID string) string {
	code := "computer/backend_failed"
	var dns *net.DNSError
	message := strings.ToLower(cause.Error())
	switch {
	case errors.Is(cause, sandbox.ErrComputerMissing):
		code = "computer/instance_missing"
	case errors.Is(cause, sandbox.ErrComputerInsecure):
		code = "computer/rebuild_required"
	case errors.Is(cause, sandbox.ErrComputerIdentity), errors.Is(cause, sandbox.ErrComputerAmbiguous):
		code = "computer/identity_mismatch"
	case errors.Is(cause, volume.ErrMissing):
		code = "volume/missing"
	case errors.Is(cause, volume.ErrConflict):
		code = "volume/conflict"
	case errors.As(cause, &dns), strings.Contains(message, "no such host"), strings.Contains(message, "name or service not known"), strings.Contains(message, "temporary failure in name resolution"):
		code = "computer/backend_dns"
	case strings.Contains(message, "egress sidecar did not become ready"):
		code = "computer/network_not_ready"
	case errors.Is(cause, context.DeadlineExceeded), strings.Contains(message, "timeout"), strings.Contains(message, "timed out"):
		code = "computer/backend_timeout"
	case errors.Is(cause, sandbox.ErrBackendAuth):
		code = "computer/backend_auth"
	case errors.Is(cause, sandbox.ErrImageUnavailable):
		code = "computer/image_unavailable"
	}
	raw, _ := json.Marshal(storedFailure{Code: code, OperationID: operationID})
	return string(raw)
}

func publicFailure(value string) *ErrorDetail {
	if value == "" {
		return nil
	}
	var stored storedFailure
	_ = json.Unmarshal([]byte(value), &stored)
	if value == "recovery pending: termination or cleanup not yet confirmed" {
		stored.Code = "computer/recovery_pending"
	}
	texts, known := failureMessages[stored.Code]
	if !known {
		stored.Code = "computer/legacy_failure"
		texts = failureMessages[stored.Code]
	}
	return &ErrorDetail{Code: stored.Code, Message: texts[0], Suggestion: texts[1], OperationID: stored.OperationID}
}

func publicErrorMessage(detail *ErrorDetail) string {
	if detail == nil {
		return ""
	}
	return detail.Message
}
