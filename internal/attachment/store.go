// Package attachment 实现对话图片附件：内容寻址不可变存储（字节去重）、
// 图片校验（魔数嗅探/宽高/像素上限）与附件元数据（attachment 表）。
// 附件永不自动删除（fork 共享同一不可变对象），引用感知 GC 后续另做。
package attachment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// BlobStore 内容寻址 blob 存储：<root>/<sha256[:2]>/<sha256>，不可变、字节去重。
type BlobStore struct {
	root string
}

func NewBlobStore(root string) *BlobStore {
	return &BlobStore{root: root}
}

// ErrCorrupt 读时内容校验失败（blob 缺失/损坏/被替换）。
var ErrCorrupt = errors.New("attachment: blob 内容校验失败")

// Put 写入 blob 并返回 sha256 hex；同内容已存在时直接命中去重。
func (s *BlobStore) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	hexSum := hex.EncodeToString(sum[:])
	dir := filepath.Join(s.root, hexSum[:2])
	final := filepath.Join(dir, hexSum)
	if _, err := os.Stat(final); err == nil {
		return hexSum, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpName, final); err != nil {
		// Windows 下目标已存在时 rename 失败；并发写同内容按去重成功处理。
		if _, statErr := os.Stat(final); statErr == nil {
			return hexSum, nil
		}
		return "", err
	}
	return hexSum, nil
}

// Get 读取 blob 并按 hex 校验内容（篡改/损坏检测）。
func (s *BlobStore) Get(hexSum string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(s.root, hexSum[:2], hexSum))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hexSum {
		return nil, fmt.Errorf("%w: sha256:%s", ErrCorrupt, hexSum)
	}
	return data, nil
}
