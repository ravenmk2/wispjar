package attachment

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodeGIF(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pngHeader 只含签名 + IHDR 的合法 PNG 头（CRC 有效），用于构造超大尺寸声明。
func pngHeader(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // color type RGB
	out := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	chunk := append([]byte("IHDR"), ihdr...)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 13)
	out = append(out, lenBuf[:]...)
	out = append(out, chunk...)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc32.ChecksumIEEE(chunk))
	return append(out, crcBuf[:]...)
}

func webpVP8X(w, h int) []byte {
	out := []byte("RIFF\x1a\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00")
	put24 := func(v int) { out = append(out, byte(v), byte(v>>8), byte(v>>16)) }
	put24(w - 1)
	put24(h - 1)
	return out
}

func webpVP8L(w, h int) []byte {
	out := []byte("RIFF\x11\x00\x00\x00WEBPVP8L\x05\x00\x00\x00\x2f")
	bits := uint32(w-1) | uint32(h-1)<<14
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], bits)
	return append(out, b[:]...)
}

func TestSniffMIME(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"png", encodePNG(t, 2, 2), "image/png"},
		{"jpeg", encodeJPEG(t, 2, 2), "image/jpeg"},
		{"gif", encodeGIF(t, 2, 2), "image/gif"},
		{"webp-vp8x", webpVP8X(2, 2), "image/webp"},
		{"riff-not-webp", []byte("RIFF\x00\x00\x00\x00WAVExxxx"), ""},
		{"bmp", []byte("BM\x00\x00\x00\x00\x00\x00"), ""},
		{"text", []byte("hello world, definitely not an image"), ""},
		{"empty", nil, ""},
	}
	for _, tc := range cases {
		got, ok := sniffMIME(tc.data)
		if tc.want == "" {
			if ok {
				t.Fatalf("%s: want reject, got %q", tc.name, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Fatalf("%s: want %q, got %q ok=%v", tc.name, tc.want, got, ok)
		}
	}
}

func TestValidateImageDimensions(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		mime string
		w, h int
	}{
		{"png", encodePNG(t, 3, 5), "image/png", 3, 5},
		{"jpeg", encodeJPEG(t, 7, 4), "image/jpeg", 7, 4},
		{"gif", encodeGIF(t, 6, 9), "image/gif", 6, 9},
		{"webp-vp8x", webpVP8X(640, 480), "image/webp", 640, 480},
		{"webp-vp8l", webpVP8L(320, 200), "image/webp", 320, 200},
	}
	for _, tc := range cases {
		mime, w, h, err := ValidateImage(tc.data)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if mime != tc.mime || w != tc.w || h != tc.h {
			t.Fatalf("%s: want (%s,%d,%d), got (%s,%d,%d)", tc.name, tc.mime, tc.w, tc.h, mime, w, h)
		}
	}
}

func TestValidateImagePixelLimit(t *testing.T) {
	// 8193x8193 > 64M 像素（声明即可，无需真实像素数据）。
	if _, _, _, err := ValidateImage(pngHeader(8193, 8193)); err == nil {
		t.Fatal("want pixel limit error")
	}
	if _, _, _, err := ValidateImage(pngHeader(100, 100)); err != nil {
		t.Fatalf("small png must pass: %v", err)
	}
}

func TestValidateImageRejects(t *testing.T) {
	if _, _, _, err := ValidateImage(nil); err == nil {
		t.Fatal("want error for empty input")
	}
	if _, _, _, err := ValidateImage([]byte("%PDF-1.4 not an image")); err == nil {
		t.Fatal("want error for non-image content")
	}
	if _, _, _, err := ValidateImage(pngHeader(0, 10)); err == nil {
		t.Fatal("want error for zero dimension")
	}
}
