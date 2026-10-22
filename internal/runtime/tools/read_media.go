package tools

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/session"
)

// ImageSaver 图片字节入库（内容寻址存储 + attachment 表；*attachment.Service 实现）。
// 同字节去重，幂等返回既有引用。
type ImageSaver interface {
	Upload(ctx context.Context, orgID, actorID, name string, data []byte) (*session.AttachmentRef, error)
}

type readMediaArgs struct {
	Path         string `json:"path,omitempty" jsonschema:"description=image file path; provide either path or attachment_id"`
	AttachmentID string `json:"attachment_id,omitempty" jsonschema:"description=persistent attachment ID from an earlier image result; mutually exclusive with path"`
}

// textResult 可恢复的域拒绝（vision 门控/非图/超限）：以文本工具结果回喂模型而非 Go error
// （Go error 会被 eino ToolsNode 升级为整个 run 失败；对齐 DSH 的可恢复工具错误）。
func textResult(msg string) *schema.ToolResult {
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: msg}}}
}

// newReadMediaTool 构建 read_media（vision 为构建期捕获的当前 Agent 模型图片输入能力；
// 主/子 Agent 各自构建，门控按各自模型判定）。enhanced 工具：tool 消息直接携带
// 文本信封 + 图片 part（eino ToolResult → ToolsNode 写入 UserInputMultiContent）。
func newReadMediaTool(vision bool) (tool.BaseTool, error) {
	return utils.InferEnhancedTool("read_media",
		"Read a PNG/JPEG/WebP/GIF image file and return the image itself for viewing. Use this to look at screenshots, photos, charts or other image files; the read tool only handles text.",
		func(ctx context.Context, in readMediaArgs) (*schema.ToolResult, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderRead)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()

			// vision 门控先于任何 I/O（读图只为让模型看见；纯文本模型看见的是空气）。
			if !vision {
				return textResult("read_media requires vision, tools, tool_images and a supported Responses file_id protocol; switch models and retry"), nil
			}
			if tc.Images == nil {
				return textResult("attachment service is not configured; read_media is unavailable"), nil
			}
			if (in.Path == "") == (in.AttachmentID == "") {
				return textResult("provide exactly one of path or attachment_id"), nil
			}
			if in.AttachmentID != "" {
				reader, ok := tc.Images.(interface {
					Resolve(context.Context, string, []string) ([]session.AttachmentRef, error)
				})
				if !ok {
					return textResult("persistent attachment reader is unavailable"), nil
				}
				refs, err := reader.Resolve(ctx, tc.OrgID, []string{in.AttachmentID})
				if err != nil {
					return textResult("attachment does not exist or is not accessible"), nil
				}
				meta.Attachments = refs
				handle, err := marshalResult(map[string]any{"attachment_id": refs[0].ID, "mime": refs[0].MIME, "bytes": refs[0].Bytes})
				if err != nil {
					return nil, err
				}
				return mediaResult(handle, refs[0]), nil
			}
			h, sid, err := tc.sandboxHandle(ctx)
			if err != nil {
				return nil, err
			}
			meta.SandboxID = sid
			in.Path = tc.ResolvePath(in.Path)
			downscaleAdvice := fmt.Sprintf("; downscale it with exec and read again (e.g. convert %s -resize 2048x2048 /tmp/scaled.png)", shellQuote(in.Path))
			if fi, err := h.FileInfo(ctx, in.Path); err == nil && fi.Size > attachment.MaxImageBytes {
				return textResult(fmt.Sprintf("image %s is %d bytes, exceeding the 8MB limit%s", in.Path, fi.Size, downscaleAdvice)), nil
			}
			data, err := h.DownloadFile(ctx, in.Path)
			if err != nil {
				return nil, fmt.Errorf("read_media %s: %w", in.Path, err)
			}
			mime, w, height, err := attachment.ValidateImage(data)
			if err != nil {
				var ie *attachment.ImageError
				if errors.As(err, &ie) {
					// 闸值拒绝映射为英文（ImageError.Message 是用户上传侧的中文 REST 文案，
					// 不透传给模型）；非 ImageError 的未知错误走通用句式。
					switch ie.Kind {
					case attachment.GateTooLarge:
						return textResult(fmt.Sprintf("image %s is %d bytes, exceeding the 8MB limit%s", in.Path, len(data), downscaleAdvice)), nil
					case attachment.GateTooManyPixels:
						return textResult(fmt.Sprintf("image %s exceeds the 64M pixel limit%s", in.Path, downscaleAdvice)), nil
					case attachment.GateNotImage:
						return textResult(fmt.Sprintf("%s is not a supported image (only PNG / JPEG / WebP / GIF)", in.Path)), nil
					case attachment.GateDecodeFailed:
						return textResult(fmt.Sprintf("%s: failed to decode image", in.Path)), nil
					}
				}
				return textResult(fmt.Sprintf("%s is not a readable image: %s", in.Path, err.Error())), nil
			}
			ref, err := tc.Images.Upload(ctx, tc.OrgID, tc.UserID, path.Base(in.Path), data)
			if err != nil {
				return nil, fmt.Errorf("read_media %s: failed to store image: %w", in.Path, err)
			}
			meta.Attachments = []session.AttachmentRef{*ref}

			short := strings.TrimPrefix(ref.ID, "sha256:")
			if len(short) > 8 {
				short = short[:8]
			}
			handle, err := marshalResult(map[string]any{
				"path":          in.Path,
				"mime":          mime,
				"width":         w,
				"height":        height,
				"bytes":         len(data),
				"sha256":        short,
				"attachment_id": ref.ID,
			})
			if err != nil {
				return nil, err
			}
			return mediaResult(handle, *ref), nil
		})
}

func mediaResult(text string, ref session.AttachmentRef) *schema.ToolResult {
	return &schema.ToolResult{Parts: []schema.ToolOutputPart{
		{Type: schema.ToolPartTypeText, Text: text},
		{Type: schema.ToolPartTypeImage, Image: &schema.ToolOutputImage{MessagePartCommon: attachment.ImageReference(ref)}},
	}}
}

// ReadMedia 单独构建 read_media（tools_enabled 启用时由 runtime 注册；vision 按 Agent 模型判定）。
func ReadMedia(vision bool) (tool.BaseTool, error) { return newReadMediaTool(vision) }
