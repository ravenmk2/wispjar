package opensandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"

	opensandbox "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
)

// UserFiles never falls back to the privileged maintenance filesystem.
func (h *Handle) UserFiles() (FileReader, error) {
	if h.downloadClient == nil {
		return nil, ErrFileAPIUnavailable
	}
	client, err := h.downloadClient.FilesWithIdentity(65532, 65532)
	if err != nil {
		return nil, err
	}
	return nativeFileReader{client}, nil
}

type nativeFileReader struct {
	client opensandbox.IdentityFilesystem
}

func nativeFileError(err error) error {
	var api *opensandbox.APIError
	if errors.As(err, &api) {
		switch {
		case api.StatusCode == 404 && api.Response.Code == "FILE_NOT_FOUND":
			return fmt.Errorf("%w: %v", fs.ErrNotExist, err)
		case api.StatusCode == 404:
			return fmt.Errorf("%w: %v", ErrFileAPIUnavailable, err)
		case api.StatusCode == 403:
			return fmt.Errorf("%w: %v", fs.ErrPermission, err)
		}
	}
	return err
}

func entry(info opensandbox.FileInfo) FileEntry {
	return FileEntry{Path: info.Path, Kind: info.Type, Size: info.Size, ModifiedAt: info.ModifiedAt}
}

func (f nativeFileReader) List(ctx context.Context, path string) ([]FileEntry, error) {
	items, err := f.client.ListDirectory(ctx, path)
	if err != nil {
		return nil, nativeFileError(err)
	}
	result := make([]FileEntry, 0, len(items))
	for _, info := range items {
		result = append(result, entry(info))
	}
	return result, nil
}

func (f nativeFileReader) Stat(ctx context.Context, path string) (FileEntry, error) {
	items, err := f.client.GetFileInfo(ctx, path)
	if err != nil {
		return FileEntry{}, nativeFileError(err)
	}
	info, ok := items[path]
	if !ok {
		return FileEntry{}, fs.ErrNotExist
	}
	return entry(info), nil
}

func (f nativeFileReader) Open(ctx context.Context, path string, limit int64) (io.ReadCloser, error) {
	if limit < 1 {
		return nil, fmt.Errorf("invalid file read limit")
	}
	body, err := f.client.DownloadFile(ctx, path, fmt.Sprintf("bytes=0-%d", limit-1))
	return body, nativeFileError(err)
}
