package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ravenmk2/wispjar/internal/attachment"
	"github.com/ravenmk2/wispjar/internal/session"
	"github.com/ravenmk2/wispjar/internal/store"
)

const mediaPolicy = "file-reference-v1"

type mediaOrgKey struct{}

func WithMediaOrg(ctx context.Context, orgID string) context.Context {
	return context.WithValue(ctx, mediaOrgKey{}, orgID)
}

type MediaReader interface {
	Resolve(context.Context, string, []string) ([]session.AttachmentRef, error)
	ReadForOrg(context.Context, string, string) ([]byte, string, error)
}

type mediaStore struct {
	db     *gorm.DB
	reader MediaReader
	once   sync.Once
	locks  [64]chan struct{}
	memory sync.Map
}

func mediaHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func referenceID(part schema.MessageInputPart) string {
	if part.Image == nil || part.Image.URL == nil {
		return ""
	}
	return strings.TrimPrefix(*part.Image.URL, attachment.ReferencePrefix)
}

func (s *mediaStore) project(ctx context.Context, messages []*schema.Message, toolImages bool) ([]*schema.Message, error) {
	orgID, _ := ctx.Value(mediaOrgKey{}).(string)
	type imagePosition struct {
		message, part int
		ref           session.AttachmentRef
		bytes         int64
	}
	positions := []imagePosition{}
	refs := map[string]session.AttachmentRef{}
	result := make([]*schema.Message, len(messages))
	var total int64
	for index, message := range messages {
		copied := *message
		copied.UserInputMultiContent = append([]schema.MessageInputPart(nil), message.UserInputMultiContent...)
		result[index] = &copied
		for partIndex, part := range copied.UserInputMultiContent {
			if part.Image == nil {
				continue
			}
			if message.Role == schema.Tool && !toolImages {
				return nil, errors.New("provider: this route does not support native tool images; select a Responses tool_images model")
			}
			ref := session.AttachmentRef{}
			var size int64
			if part.Image.URL != nil && strings.HasPrefix(*part.Image.URL, attachment.ReferencePrefix) {
				if orgID == "" || s == nil || s.reader == nil {
					return nil, errors.New("provider: missing attachment tenant context")
				}
				id := referenceID(part)
				var ok bool
				ref, ok = refs[id]
				if !ok {
					resolved, err := s.reader.Resolve(ctx, orgID, []string{id})
					if err != nil {
						return nil, fmt.Errorf("provider: resolve image: %w", err)
					}
					if len(resolved) != 1 {
						return nil, errors.New("provider: attachment missing")
					}
					ref = resolved[0]
					refs[id] = ref
				}
				size = (ref.Bytes + 2) / 3 * 4
			} else if part.Image.Base64Data != nil && message.Role != schema.Tool {
				size = int64(len(*part.Image.Base64Data))
			} else {
				return nil, errors.New("provider: only persistent attachments are accepted for tool images; external image URLs are not durable")
			}
			positions = append(positions, imagePosition{index, partIndex, ref, size})
			total += size
		}
	}
	drop := 0
	if len(positions) > 8 {
		drop = (len(positions) - 8 + 1) / 2 * 2
	}
	var freed int64
	for index := 0; index < drop; index++ {
		freed += positions[index].bytes
	}
	if total > 16<<20 {
		target := (total - (16 << 20) + (4 << 20) - 1) / (4 << 20) * (4 << 20)
		for drop < len(positions) && freed < target {
			freed += positions[drop].bytes
			drop++
		}
		drop = (drop + 1) / 2 * 2
	}
	if drop > len(positions) {
		drop = len(positions)
	}
	for _, position := range positions[:drop] {
		text := fmt.Sprintf("[image offloaded] attachment_id=%s; use read_media with attachment_id to view again (%s).", position.ref.ID, mediaPolicy)
		result[position.message].UserInputMultiContent[position.part] = schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: text}
	}
	return result, nil
}

type inlineMediaModel struct {
	model.ToolCallingChatModel
	media *mediaStore
}

func (m *inlineMediaModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.ToolCallingChatModel.WithTools(infos)
	if err != nil {
		return nil, err
	}
	return &inlineMediaModel{ToolCallingChatModel: bound, media: m.media}, nil
}

func (m *inlineMediaModel) input(ctx context.Context, messages []*schema.Message) ([]*schema.Message, error) {
	projected, err := m.media.project(ctx, messages, false)
	if err != nil {
		return nil, err
	}
	orgID, _ := ctx.Value(mediaOrgKey{}).(string)
	for _, message := range projected {
		for index, part := range message.UserInputMultiContent {
			if part.Image == nil || part.Image.URL == nil {
				continue
			}
			data, mime, err := m.media.reader.ReadForOrg(ctx, orgID, referenceID(part))
			if err != nil {
				return nil, err
			}
			encoded := base64.StdEncoding.EncodeToString(data)
			part.Image = &schema.MessageInputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &encoded, MIMEType: mime}}
			message.UserInputMultiContent[index] = part
		}
	}
	return projected, nil
}

func (m *inlineMediaModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	input, err := m.input(ctx, messages)
	if err != nil {
		return nil, err
	}
	return m.ToolCallingChatModel.Generate(ctx, input, opts...)
}

func (m *inlineMediaModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	input, err := m.input(ctx, messages)
	if err != nil {
		return nil, err
	}
	return m.ToolCallingChatModel.Stream(ctx, input, opts...)
}

type remoteFile struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ExpiresAt int64  `json:"expires_at"`
}

type apiError struct {
	status        int
	code, message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("provider: HTTP %d (%s): %s", e.status, e.code, e.message)
}

func readAPIError(resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
	code := body.Error.Code
	if code == "" {
		code = body.Error.Type
	}
	return &apiError{resp.StatusCode, code, body.Error.Message}
}

func (m *responsesModel) scope(ctx context.Context) string {
	orgID, _ := ctx.Value(mediaOrgKey{}).(string)
	data, _ := json.Marshal([]string{orgID, m.providerID, m.providerType, m.baseURL, m.apiKey, mediaPolicy})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *mediaStore) cached(ctx context.Context, scope, id string) (*store.ProviderMediaFile, error) {
	if s.db == nil {
		if cached, ok := s.memory.Load(scope + id); ok {
			row := cached.(store.ProviderMediaFile)
			return &row, nil
		}
		return nil, nil
	}
	var row store.ProviderMediaFile
	err := s.db.WithContext(ctx).Where("scope = ? AND attachment_id = ?", scope, id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

func (s *mediaStore) invalidate(ctx context.Context, scope, id, fileID string) error {
	if s.db == nil {
		if cached, ok := s.memory.Load(scope + id); ok && cached.(store.ProviderMediaFile).FileID == fileID {
			s.memory.CompareAndDelete(scope+id, cached)
		}
		return nil
	}
	return s.db.WithContext(ctx).Where("scope = ? AND attachment_id = ? AND file_id = ?", scope, id, fileID).Delete(&store.ProviderMediaFile{}).Error
}

func (m *responsesModel) fileID(ctx context.Context, id string) (string, error) {
	s := m.media
	scope := m.scope(ctx)
	s.once.Do(func() {
		for index := range s.locks {
			s.locks[index] = make(chan struct{}, 1)
		}
	})
	sum := sha256.Sum256([]byte(scope + id))
	lock := s.locks[int(sum[0])%len(s.locks)]
	select {
	case lock <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-lock }()
	row, err := s.cached(ctx, scope, id)
	if err != nil {
		return "", err
	}
	if row != nil && (row.ExpiresAt == 0 || row.ExpiresAt > time.Now().Add(5*time.Minute).Unix()) {
		return row.FileID, nil
	}
	orgID, _ := ctx.Value(mediaOrgKey{}).(string)
	data, mime, err := s.reader.ReadForOrg(ctx, orgID, id)
	if err != nil {
		return "", err
	}
	file, err := m.upload(ctx, id, mime, data)
	if err != nil {
		return "", err
	}
	row = &store.ProviderMediaFile{Scope: scope, AttachmentID: id, FileID: file.ID, ExpiresAt: file.ExpiresAt}
	if s.db == nil {
		s.memory.Store(scope+id, *row)
	} else {
		err = s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}, {Name: "attachment_id"}}, DoUpdates: clause.AssignmentColumns([]string{"file_id", "expires_at"})}).Create(row).Error
	}
	return file.ID, err
}

func (m *responsesModel) upload(ctx context.Context, id, mime string, data []byte) (*remoteFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{"purpose": "user_data"}
	if m.providerType != "ark" {
		fields["expires_after[anchor]"] = "created_at"
		fields["expires_after[seconds]"] = "86400"
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, err
		}
	}
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[mime]
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s%s"`, strings.TrimPrefix(id, "sha256:"), ext))
	header.Set("Content-Type", mime)
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, err
	}
	if _, err = part.Write(data); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	resp, err := m.request(ctx, http.MethodPost, "/files", &body, writer.FormDataContentType())
	if err != nil {
		return nil, err
	}
	file := &remoteFile{}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(file)
	_ = resp.Body.Close()
	if err != nil || file.ID == "" {
		return nil, errors.New("provider: invalid file upload response")
	}
	if m.providerType != "ark" && file.ExpiresAt == 0 {
		file.ExpiresAt = time.Now().Add(24 * time.Hour).Unix()
	}
	for m.providerType == "ark" && file.Status != "active" {
		if file.Status != "processing" && file.Status != "pending" {
			return nil, fmt.Errorf("provider: file is not active (%s)", file.Status)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
		resp, err = m.request(ctx, http.MethodGet, "/files/"+url.PathEscape(file.ID), nil, "")
		if err != nil {
			return nil, err
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(file)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	return file, nil
}

func (m *responsesModel) request(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(m.baseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, readAPIError(resp)
	}
	return resp, nil
}
