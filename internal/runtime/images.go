package runtime

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/ravenmk2/wispjar/internal/session"
)

// image-offload-v1 卸载规则（版本化常量）。
// 规则只依赖已提交历史，同一历史前缀永远产出同一请求前缀（缓存稳定）；
// 任何规则变更 = 历史前缀失效，必须整体更换版本号并在《缓存命中优先设计原则》下重审。
const imageOffloadVersion = "image-offload-v1"

const (
	offloadMaxImages    = 8        // 每请求最多随历史发送的图片张数
	offloadMaxBytes     = 16 << 20 // 图片 base64 后合计字节预算
	offloadCountQuantum = 2        // 卸载张数向上取整到该量子的倍数
	offloadByteQuantum  = 4 << 20  // 卸载字节目标向上取整到该量子的倍数
)

// base64Len base64 编码后的字节数（不读 blob，按原始字节数推算）。
func base64Len(n int64) int {
	return int((n + 2) / 3 * 4)
}

func roundUp(n, quantum int) int {
	return (n + quantum - 1) / quantum * quantum
}

// planImageOffload 计算卸载集：返回与 msgs 对齐的 per-attachment 标记（true=卸载）。
// 最旧优先 + 量子化：张数超限部分向上取整到 2 的倍数；字节超限的卸载目标
// 向上取整到 4MB 的倍数，最终卸载张数再按 2 取整（确定性，边界不逐轮漂移）。
func planImageOffload(msgs []session.Message) [][]bool {
	type occurrence struct{ msg, att, b64 int }
	occs := []occurrence{}
	total := 0
	for i, m := range msgs {
		for j, a := range m.Attachments {
			b64 := base64Len(a.Bytes)
			occs = append(occs, occurrence{i, j, b64})
			total += b64
		}
	}
	marks := make([][]bool, len(msgs))
	if len(occs) == 0 {
		return marks
	}
	drop := 0
	if len(occs) > offloadMaxImages {
		drop = roundUp(len(occs)-offloadMaxImages, offloadCountQuantum)
	}
	if total > offloadMaxBytes {
		target := roundUp(total-offloadMaxBytes, offloadByteQuantum)
		freed := 0
		for k := 0; k < drop && k < len(occs); k++ {
			freed += occs[k].b64
		}
		for drop < len(occs) && freed < target {
			freed += occs[drop].b64
			drop++
		}
		if drop = roundUp(drop, offloadCountQuantum); drop > len(occs) {
			drop = len(occs)
		}
	}
	for k := 0; k < drop; k++ {
		o := occs[k]
		if marks[o.msg] == nil {
			marks[o.msg] = make([]bool, len(msgs[o.msg].Attachments))
		}
		marks[o.msg][o.att] = true
	}
	return marks
}

// offloadedImageText 卸载位的确定性占位文本（含附件身份；无沙箱读回通道，注明可请用户重发）。
func offloadedImageText(ref session.AttachmentRef) string {
	short := strings.TrimPrefix(ref.ID, "sha256:")
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("[图片已卸载] %q (sha256:%s)：因超出单次请求图片预算未随历史发送；如需模型查看该图片，请提醒用户重新发送。", ref.Name, short)
}

// buildUserMessage 组装带附件的 user 消息：图片在前、文本在后（ark 官方建议顺序；
// 固定顺序保证逐字节确定性）。图片用 Base64Data+MIME 内联（三家供应商唯一共同路径）。
func (o *orchestrator) buildUserMessage(ctx context.Context, m session.Message, offloaded []bool) (*schema.Message, error) {
	if o.deps.Attachments == nil {
		return nil, fmt.Errorf("会话历史包含图片附件，但未配置附件存储")
	}
	parts := make([]schema.MessageInputPart, 0, len(m.Attachments)+1)
	for j, ref := range m.Attachments {
		if j < len(offloaded) && offloaded[j] {
			parts = append(parts, schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeText, Text: offloadedImageText(ref),
			})
			continue
		}
		data, err := o.deps.Attachments.ReadBytes(ctx, ref.ID)
		if err != nil {
			return nil, fmt.Errorf("读取附件 %s 失败: %w", ref.ID, err)
		}
		b64 := base64.StdEncoding.EncodeToString(data)
		parts = append(parts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{Base64Data: &b64, MIMEType: ref.MIME},
			},
		})
	}
	parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: m.Content})
	return &schema.Message{Role: schema.User, UserInputMultiContent: parts}, nil
}
