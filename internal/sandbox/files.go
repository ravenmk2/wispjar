package sandbox

import (
	"context"
	"fmt"
	"io"
	"time"
)

type FileEntry struct {
	Path, Kind string
	Size       int64
	ModifiedAt time.Time
}

// FileReader uses absolute sandbox paths and workload credentials. UI scopes
// are navigation roots. Open callers close the stream and bound their reads.
type FileReader interface {
	List(context.Context, string) ([]FileEntry, error)
	Stat(context.Context, string) (FileEntry, error)
	Open(context.Context, string, int64) (io.ReadCloser, error)
}

var ErrFileAPIUnavailable = fmt.Errorf("%w: native file API unavailable", ErrUnsupported)
