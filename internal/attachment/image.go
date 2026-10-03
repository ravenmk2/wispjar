package attachment

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

const (
	// MaxImageBytes 单张原图上限（8MB；base64 后约 10.7MB，留各供应商编码后 10MB 档位的余量由卸载预算兜底）。
	MaxImageBytes = 8 << 20
	// MaxPixels 解码后总像素上限。
	MaxPixels = 64 << 20
)

// sniffMIME 按魔数嗅探图片类型（不信客户端声明），仅放行 png/jpeg/webp/gif。
func sniffMIME(data []byte) (string, bool) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png", true
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg", true
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return "image/gif", true
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return "image/webp", true
	}
	return "", false
}

// validateImage 校验并返回 (mime, width, height)：魔数白名单 → 解码宽高 → 像素上限。
func validateImage(data []byte) (string, int, int, error) {
	if len(data) == 0 {
		return "", 0, 0, errors.New("空文件")
	}
	if len(data) > MaxImageBytes {
		return "", 0, 0, fmt.Errorf("图片大小 %d 字节超过 8MB 限制", len(data))
	}
	mime, ok := sniffMIME(data)
	if !ok {
		return "", 0, 0, errors.New("仅支持 PNG / JPEG / WebP / GIF 图片（按文件内容识别）")
	}
	w, h, err := imageDimensions(data, mime)
	if err != nil {
		return "", 0, 0, fmt.Errorf("图片解码失败: %v", err)
	}
	if w < 1 || h < 1 || int64(w)*int64(h) > MaxPixels {
		return "", 0, 0, fmt.Errorf("图片尺寸 %dx%d 超出 64M 像素限制", w, h)
	}
	return mime, w, h, nil
}

func imageDimensions(data []byte, mime string) (int, int, error) {
	if mime == "image/webp" {
		return webpDimensions(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// webpDimensions 解析 WebP 容器宽高（VP8/VP8L/VP8X 三种格式）。
func webpDimensions(data []byte) (int, int, error) {
	if len(data) < 20 {
		return 0, 0, errors.New("webp 数据过短")
	}
	switch string(data[12:16]) {
	case "VP8 ":
		if len(data) < 30 || data[23] != 0x9d || data[24] != 0x01 || data[25] != 0x2a {
			return 0, 0, errors.New("webp VP8 帧头无效")
		}
		w := int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
		return w, h, nil
	case "VP8L":
		if len(data) < 25 || data[20] != 0x2f {
			return 0, 0, errors.New("webp VP8L 帧头无效")
		}
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3fff) + 1, int(bits>>14&0x3fff) + 1, nil
	case "VP8X":
		if len(data) < 30 {
			return 0, 0, errors.New("webp VP8X 帧头无效")
		}
		w := 1 + int(data[24]) | int(data[25])<<8 | int(data[26])<<16
		h := 1 + int(data[27]) | int(data[28])<<8 | int(data[29])<<16
		return w, h, nil
	}
	return 0, 0, fmt.Errorf("不支持的 webp 子格式 %q", string(data[12:16]))
}
