package tools

import (
	"context"
	"encoding/base64"
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
	Path string `json:"path" jsonschema:"description=absolute image file path inside the sandbox"`
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
		"Read a PNG/JPEG/WebP/GIF image file from the sandbox and return the image itself for viewing. Use this to look at screenshots, photos, charts or other image files; the read tool only handles text.",
		func(ctx context.Context, in readMediaArgs) (*schema.ToolResult, error) {
			tc := FromContext(ctx)
			meta := tc.beginMeta(ctx, RenderRead)
			start := time.Now()
			defer func() { meta.DurationMS = sinceMS(start) }()

			// vision 门控先于任何 I/O（读图只为让模型看见；纯文本模型看见的是空气）。
			if !vision {
				return textResult("当前模型不支持图片输入（capabilities.vision=false），read_media 不可用；请切换到支持视觉的模型后再试"), nil
			}
			if tc.Images == nil {
				return textResult("附件服务未配置，read_media 不可用"), nil
			}
			h, sid, err := tc.sandboxHandle(ctx)
			if err != nil {
				return nil, err
			}
			meta.SandboxID = sid
			downscaleAdvice := fmt.Sprintf("；请用 exec 缩放后再读（如 convert %s -resize 2048x2048 /tmp/scaled.png）", shellQuote(in.Path))
			if fi, err := h.FileInfo(ctx, in.Path); err == nil && fi.Size > attachment.MaxImageBytes {
				return textResult(fmt.Sprintf("图片 %s 为 %d 字节，超过 8MB 上限%s", in.Path, fi.Size, downscaleAdvice)), nil
			}
			data, err := h.DownloadFile(ctx, in.Path)
			if err != nil {
				return nil, fmt.Errorf("read_media %s: %w", in.Path, err)
			}
			mime, w, height, err := attachment.ValidateImage(data)
			if err != nil {
				var ie *attachment.ImageError
				if errors.As(err, &ie) && (ie.Kind == attachment.GateTooLarge || ie.Kind == attachment.GateTooManyPixels) {
					return textResult(err.Error() + downscaleAdvice), nil
				}
				return textResult(fmt.Sprintf("%s 不是可读取的图片: %s", in.Path, err.Error())), nil
			}
			ref, err := tc.Images.Upload(ctx, tc.OrgID, tc.UserID, path.Base(in.Path), data)
			if err != nil {
				return nil, fmt.Errorf("read_media %s 入库失败: %w", in.Path, err)
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
			b64 := base64.StdEncoding.EncodeToString(data)
			return &schema.ToolResult{Parts: []schema.ToolOutputPart{
				{Type: schema.ToolPartTypeText, Text: handle},
				{Type: schema.ToolPartTypeImage, Image: &schema.ToolOutputImage{
					MessagePartCommon: schema.MessagePartCommon{Base64Data: &b64, MIMEType: mime},
				}},
			}}, nil
		})
}

// ReadMedia 单独构建 read_media（tools_enabled 启用时由 runtime 注册；vision 按 Agent 模型判定）。
func ReadMedia(vision bool) (tool.BaseTool, error) { return newReadMediaTool(vision) }
