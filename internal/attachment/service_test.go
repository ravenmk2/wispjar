package attachment

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ravenmk2/wispjar/internal/store"
)

func makeID(b byte) string {
	return "sha256:" + strings.Repeat(string(b), 64)
}

func TestResolveTooMany(t *testing.T) {
	svc := &Service{}
	ids := make([]string, MaxPerMessage+1)
	for i := range ids {
		ids[i] = makeID('a' + byte(i))
	}
	_, err := svc.Resolve(context.Background(), "org-1", ids)
	var ae *Error
	if !errors.As(err, &ae) || ae.Status != http.StatusBadRequest {
		t.Fatalf("want 400 for >9 attachments, got %v", err)
	}
}

func TestResolveBadIDFormat(t *testing.T) {
	svc := &Service{}
	for _, id := range []string{"", "abc", "sha256:XYZ", "md5:" + strings.Repeat("a", 32), makeID('A')} {
		if _, err := svc.Resolve(context.Background(), "org-1", []string{id}); err == nil {
			t.Fatalf("want format error for id %q", id)
		}
	}
}

func TestResolveNotFoundAndCrossOrg(t *testing.T) {
	row := store.Attachment{
		ID: makeID('a'), OrgID: "org-1", MIME: "image/png",
		Bytes: 10, Width: 2, Height: 2, Name: "a.png",
	}
	svc := &Service{loadRowsFn: func(_ context.Context, ids []string) ([]store.Attachment, error) {
		return []store.Attachment{row}, nil
	}}

	// 不存在：查询结果没有该 id
	if _, err := svc.Resolve(context.Background(), "org-1", []string{makeID('b')}); err == nil {
		t.Fatal("want error for missing attachment")
	}
	// 跨 org：存在但 org_id 不一致
	if _, err := svc.Resolve(context.Background(), "org-2", []string{makeID('a')}); err == nil {
		t.Fatal("want error for cross-org attachment")
	}
	// 正常：按输入序返回
	refs, err := svc.Resolve(context.Background(), "org-1", []string{makeID('a')})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != makeID('a') || refs[0].MIME != "image/png" || refs[0].Name != "a.png" {
		t.Fatalf("unexpected refs: %+v", refs)
	}
}
