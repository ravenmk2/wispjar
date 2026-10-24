package runtime

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"time"

	"github.com/ravenmk2/wispjar/internal/sandbox"
	"github.com/ravenmk2/wispjar/internal/session"
)

func fileContent(result fileResponse) ([]byte, error) {
	if result.Content != nil {
		return result.Content, nil
	}
	return base64.StdEncoding.DecodeString(result.Data)
}

func (a *fileAccess) fileOperation(ctx context.Context, root, relative, op string, extra map[string]any) (fileResponse, error) {
	if op != "list" && op != "read" && op != "stat" {
		if err := a.ensureMaintenance(); err != nil {
			return fileResponse{}, err
		}
		return remoteFileOperation(ctx, a.execution, root, relative, op, extra)
	}
	if a.instance == nil {
		return fileResponse{}, sandbox.ErrFileAPIUnavailable
	}
	files, err := a.instance.Files()
	if err != nil {
		return fileResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := nativeFileOperation(ctx, files, root, relative, op, extra)
	if errors.Is(err, fs.ErrNotExist) {
		if _, rootErr := files.Stat(ctx, root); errors.Is(rootErr, fs.ErrNotExist) {
			return result, &session.Error{Status: 404, Code: "file/root_not_found", Message: "文件根目录尚未创建"}
		}
		return result, &session.Error{Status: 404, Code: "file/not_found", Message: "文件不存在"}
	}
	if errors.Is(err, sandbox.ErrFileAPIUnavailable) {
		err = &session.Error{Status: 503, Code: "file/api_unavailable", Message: "当前沙箱后端不支持原生文件读取，请检查后端版本和配置"}
	} else if errors.Is(err, fs.ErrPermission) {
		err = &session.Error{Status: 403, Code: "file/forbidden", Message: "任务用户无权访问此文件"}
	}
	return result, err
}

func nativeFileOperation(ctx context.Context, files sandbox.FileReader, root, relative, op string, extra map[string]any) (fileResponse, error) {
	result := fileResponse{Root: root, Path: relative}
	name := path.Join(root, relative)
	if op == "list" {
		items, err := files.List(ctx, name)
		if err != nil {
			return result, err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
		for _, item := range items {
			kind := item.Kind
			if kind == "directory" {
				kind = "dir"
			}
			if kind != "file" && kind != "dir" {
				continue
			}
			base := path.Base(item.Path)
			result.Items = append(result.Items, map[string]any{"name": base, "path": path.Join(relative, base), "type": kind, "size": item.Size, "modified_at": item.ModifiedAt})
		}
		return result, nil
	}
	info, err := files.Stat(ctx, name)
	if err != nil {
		return result, err
	}
	result.Bytes, result.Kind = info.Size, info.Kind
	if op == "stat" {
		return result, nil
	}
	if info.Kind != "file" {
		return result, &session.Error{Status: 400, Code: "file/invalid", Message: "只能读取普通文件"}
	}
	limit := fileTransferLimit
	if value, ok := extra["max_bytes"].(int); ok {
		limit = value
	}
	if limit < 1 || limit > fileTransferLimit {
		return result, &session.Error{Status: 400, Code: "request/invalid", Message: "文件读取大小无效"}
	}
	result.Content = []byte{}
	if info.Size == 0 {
		return result, nil
	}
	body, err := files.Open(ctx, name, int64(limit)+1)
	if err != nil {
		return result, err
	}
	defer body.Close()
	result.Content, err = io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		return result, err
	}
	result.Truncated = len(result.Content) > limit
	if result.Truncated {
		result.Content = result.Content[:limit]
	}
	return result, nil
}
