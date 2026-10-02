package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/audit"
	"github.com/ravenmk2/wispjar/internal/gateway/respond"
	"github.com/ravenmk2/wispjar/internal/id"
	"github.com/ravenmk2/wispjar/internal/store"
)

const (
	StatusActive   = "active"
	StatusDisabled = "disabled"

	defaultContextSize = 64000
)

var providerTypes = map[string]bool{
	"deepseek":      true,
	"ark":           true,
	"qwen":          true,
	"openai_compat": true,
}

type Error struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *Error) Error() string { return e.Message }

func errInvalid(msg string, details any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: respond.CodeInvalid, Message: msg, Details: details}
}

func errNotFound(msg string) *Error {
	return &Error{Status: http.StatusNotFound, Code: respond.CodeNotFound, Message: msg}
}

func errConflict(msg string, details any) *Error {
	return &Error{Status: http.StatusConflict, Code: respond.CodeConflict, Message: msg, Details: details}
}

type Service struct {
	db        *gorm.DB
	masterKey []byte
}

func NewService(db *gorm.DB, masterKey []byte) *Service {
	return &Service{db: db, masterKey: masterKey}
}

type Capabilities struct {
	Tools     bool `json:"tools"`
	Reasoning bool `json:"reasoning"`
	Vision    bool `json:"vision"`
	Streaming bool `json:"streaming"`
}

type ProviderView struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	BaseURL   string         `json:"base_url"`
	MaskedKey string         `json:"masked_key"`
	Extra     map[string]any `json:"extra"`
	Status    string         `json:"status"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type ModelView struct {
	ID            string          `json:"id"`
	ProviderID    string          `json:"provider_id"`
	ProviderName  string          `json:"provider_name"`
	ModelCode     string          `json:"model_code"`
	DisplayName   string          `json:"display_name"`
	Capabilities  json.RawMessage `json:"capabilities"`
	ContextSize   int             `json:"context_size"`
	DefaultParams json.RawMessage `json:"default_params"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type WorkspaceModel struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name"`
	ModelCode    string          `json:"model_code"`
	ProviderName string          `json:"provider_name"`
	Capabilities json.RawMessage `json:"capabilities"`
	ContextSize  int             `json:"context_size"`
}

func extraMap(raw json.RawMessage) map[string]any {
	m := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func marshalExtra(m map[string]any) (json.RawMessage, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

func (s *Service) view(p store.ModelProvider) (*ProviderView, error) {
	extra := extraMap(p.Extra)
	if len(extra) == 0 {
		extra = nil
	}
	v := &ProviderView{
		ID:        p.ID,
		Name:      p.Name,
		Type:      p.Type,
		BaseURL:   p.BaseURL,
		Extra:     extra,
		Status:    p.Status,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
	}
	key, err := Decrypt(s.masterKey, p.APIKeyEnc)
	if err != nil {
		return nil, err
	}
	v.MaskedKey = MaskKey(key)
	return v, nil
}

func (s *Service) ListProviders() ([]ProviderView, error) {
	providers := []store.ModelProvider{}
	if err := s.db.Order("id DESC").Find(&providers).Error; err != nil {
		return nil, err
	}
	views := make([]ProviderView, 0, len(providers))
	for _, p := range providers {
		v, err := s.view(p)
		if err != nil {
			return nil, err
		}
		views = append(views, *v)
	}
	return views, nil
}

func (s *Service) GetProvider(providerID string) (*ProviderView, error) {
	p, err := s.findProvider(providerID)
	if err != nil {
		return nil, err
	}
	return s.view(*p)
}

func (s *Service) findProvider(providerID string) (*store.ModelProvider, error) {
	var p store.ModelProvider
	if err := s.db.First(&p, "id = ?", providerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("供应商不存在")
		}
		return nil, err
	}
	return &p, nil
}

type CreateProviderInput struct {
	Name    string
	Type    string
	BaseURL string
	APIKey  string
	Extra   json.RawMessage
	Remark  string
}

func validBaseURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

func (s *Service) validateProviderBase(name, typ, baseURL string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return errInvalid("供应商名称必填且不超过 128 字符", nil)
	}
	if !providerTypes[typ] {
		return errInvalid("type 必须是 deepseek / ark / qwen / openai_compat", map[string]any{"type": typ})
	}
	if !validBaseURL(baseURL) {
		return errInvalid("base_url 必须是 http(s) 地址", map[string]any{"base_url": baseURL})
	}
	return nil
}

func mergeExtra(raw json.RawMessage, remark string) (json.RawMessage, error) {
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, errInvalid("extra 必须是 JSON 对象", nil)
		}
	}
	if remark != "" {
		m["remark"] = remark
	}
	return marshalExtra(m)
}

func (s *Service) CreateProvider(actorID string, in CreateProviderInput) (*ProviderView, error) {
	if err := s.validateProviderBase(in.Name, in.Type, in.BaseURL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.APIKey) == "" {
		return nil, errInvalid("api_key 必填", nil)
	}
	extra, err := mergeExtra(in.Extra, strings.TrimSpace(in.Remark))
	if err != nil {
		return nil, err
	}
	enc, err := Encrypt(s.masterKey, in.APIKey)
	if err != nil {
		return nil, err
	}
	p := store.ModelProvider{
		ID:        id.New(),
		Name:      strings.TrimSpace(in.Name),
		Type:      in.Type,
		BaseURL:   strings.TrimRight(in.BaseURL, "/"),
		APIKeyEnc: enc,
		Extra:     extra,
		Status:    StatusActive,
		CreatedBy: actorID,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			ActorID: actorID,
			Action:  audit.ProviderCreate,
			Target:  p.ID,
			Detail:  map[string]any{"name": p.Name, "type": p.Type, "base_url": p.BaseURL},
		})
	})
	if err != nil {
		return nil, err
	}
	go s.runCheck(p.ID)
	return s.view(p)
}

type UpdateProviderInput struct {
	Name    *string
	Type    *string
	BaseURL *string
	APIKey  *string
	Remark  *string
	Status  *string
}

func (s *Service) UpdateProvider(actorID, providerID string, in UpdateProviderInput) (*ProviderView, error) {
	p, err := s.findProvider(providerID)
	if err != nil {
		return nil, err
	}
	updates := map[string]any{}
	detail := map[string]any{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 128 {
			return nil, errInvalid("供应商名称必填且不超过 128 字符", nil)
		}
		updates["name"] = name
		detail["name"] = name
	}
	if in.Type != nil {
		if !providerTypes[*in.Type] {
			return nil, errInvalid("type 必须是 deepseek / ark / qwen / openai_compat", map[string]any{"type": *in.Type})
		}
		updates["type"] = *in.Type
		detail["type"] = *in.Type
	}
	if in.BaseURL != nil {
		if !validBaseURL(*in.BaseURL) {
			return nil, errInvalid("base_url 必须是 http(s) 地址", map[string]any{"base_url": *in.BaseURL})
		}
		updates["base_url"] = strings.TrimRight(*in.BaseURL, "/")
		detail["base_url"] = *in.BaseURL
	}
	if in.Status != nil {
		if *in.Status != StatusActive && *in.Status != StatusDisabled {
			return nil, errInvalid("status 必须是 active 或 disabled", map[string]any{"status": *in.Status})
		}
		updates["status"] = *in.Status
		detail["status"] = *in.Status
	}
	keyChanged := in.APIKey != nil && strings.TrimSpace(*in.APIKey) != ""
	if keyChanged {
		enc, err := Encrypt(s.masterKey, *in.APIKey)
		if err != nil {
			return nil, err
		}
		updates["api_key_enc"] = enc
	}
	extraChanged := false
	if in.Remark != nil {
		extraChanged = true
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if extraChanged {
			extra := extraMap(p.Extra)
			if remark := strings.TrimSpace(*in.Remark); remark != "" {
				extra["remark"] = remark
			} else {
				delete(extra, "remark")
			}
			raw, err := marshalExtra(extra)
			if err != nil {
				return err
			}
			updates["extra"] = raw
		}
		if len(updates) > 0 {
			if err := tx.Model(p).Updates(updates).Error; err != nil {
				return err
			}
		}
		if keyChanged {
			if err := audit.Log(tx, audit.Entry{
				ActorID: actorID,
				Action:  audit.ProviderUpdateKey,
				Target:  p.ID,
				Detail:  map[string]any{"name": p.Name},
			}); err != nil {
				return err
			}
		}
		if len(detail) > 0 || extraChanged {
			return audit.Log(tx, audit.Entry{
				ActorID: actorID,
				Action:  audit.ProviderUpdate,
				Target:  p.ID,
				Detail:  detail,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	go s.runCheck(p.ID)
	return s.GetProvider(p.ID)
}

func (s *Service) DeleteProvider(actorID, providerID string) error {
	p, err := s.findProvider(providerID)
	if err != nil {
		return err
	}
	var activeCount int64
	if err := s.db.Model(&store.Model{}).
		Where("provider_id = ? AND status = ?", providerID, StatusActive).
		Count(&activeCount).Error; err != nil {
		return err
	}
	if activeCount > 0 {
		return errConflict("存在上架中的模型，无法删除供应商", map[string]any{"models": activeCount})
	}
	var total int64
	if err := s.db.Model(&store.Model{}).
		Where("provider_id = ?", providerID).
		Count(&total).Error; err != nil {
		return err
	}
	if total > 0 {
		return errConflict("存在已下架的模型，请先删除模型后再删除供应商", map[string]any{"models": total})
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(p).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			ActorID: actorID,
			Action:  audit.ProviderDelete,
			Target:  p.ID,
			Detail:  map[string]any{"name": p.Name, "type": p.Type},
		})
	})
}

func (s *Service) runCheck(providerID string) {
	var p store.ModelProvider
	if err := s.db.First(&p, "id = ?", providerID).Error; err != nil {
		return
	}
	key, err := Decrypt(s.masterKey, p.APIKeyEnc)
	if err != nil {
		return
	}
	res := Check(context.Background(), p.BaseURL, key)
	_ = s.saveLastCheck(providerID, res)
}

func (s *Service) saveLastCheck(providerID string, res CheckResult) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var p store.ModelProvider
		if err := tx.First(&p, "id = ?", providerID).Error; err != nil {
			return err
		}
		extra := extraMap(p.Extra)
		extra["last_check"] = res
		raw, err := marshalExtra(extra)
		if err != nil {
			return err
		}
		return tx.Model(&p).Update("extra", raw).Error
	})
}

func (s *Service) CheckProvider(providerID string) (*CheckResult, error) {
	p, err := s.findProvider(providerID)
	if err != nil {
		return nil, err
	}
	key, err := Decrypt(s.masterKey, p.APIKeyEnc)
	if err != nil {
		return nil, err
	}
	res := Check(context.Background(), p.BaseURL, key)
	if err := s.saveLastCheck(p.ID, res); err != nil {
		return nil, err
	}
	return &res, nil
}

type modelRow struct {
	store.Model
	ProviderName string `gorm:"column:provider_name"`
}

func (s *Service) modelQuery() *gorm.DB {
	return s.db.Model(&store.Model{}).
		Select("model.*, model_provider.name AS provider_name").
		Joins("JOIN model_provider ON model_provider.id = model.provider_id").
		Order("model.id DESC")
}

func toModelViews(rows []modelRow) []ModelView {
	views := make([]ModelView, 0, len(rows))
	for _, r := range rows {
		views = append(views, ModelView{
			ID:            r.ID,
			ProviderID:    r.ProviderID,
			ProviderName:  r.ProviderName,
			ModelCode:     r.ModelCode,
			DisplayName:   r.DisplayName,
			Capabilities:  r.Capabilities,
			ContextSize:   r.ContextSize,
			DefaultParams: r.DefaultParams,
			Status:        r.Status,
			CreatedAt:     r.CreatedAt,
			UpdatedAt:     r.UpdatedAt,
		})
	}
	return views
}

func (s *Service) ListModels(providerID string) ([]ModelView, error) {
	q := s.modelQuery()
	if providerID = strings.TrimSpace(providerID); providerID != "" {
		q = q.Where("model.provider_id = ?", providerID)
	}
	rows := []modelRow{}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	return toModelViews(rows), nil
}

func (s *Service) ListWorkspaceModels() ([]WorkspaceModel, error) {
	rows := []WorkspaceModel{}
	err := s.db.Model(&store.Model{}).
		Select("model.id, model.display_name, model.model_code, model.capabilities, model.context_size, model_provider.name AS provider_name").
		Joins("JOIN model_provider ON model_provider.id = model.provider_id").
		Where("model.status = ?", StatusActive).
		Order("model.id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func parseCapabilities(raw json.RawMessage) (json.RawMessage, error) {
	caps := Capabilities{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &caps); err != nil {
			return nil, errInvalid("capabilities 必须是 {tools, reasoning, vision, streaming} 布尔对象", nil)
		}
	}
	b, err := json.Marshal(caps)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func parseDefaultParams(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, errInvalid("default_params 必须是 JSON 对象", nil)
	}
	return raw, nil
}

type CreateModelInput struct {
	ProviderID    string
	ModelCode     string
	DisplayName   string
	Capabilities  json.RawMessage
	ContextSize   int
	DefaultParams json.RawMessage
}

func (s *Service) CreateModel(actorID string, in CreateModelInput) (*ModelView, error) {
	if _, err := s.findProvider(in.ProviderID); err != nil {
		return nil, err
	}
	modelCode := strings.TrimSpace(in.ModelCode)
	if modelCode == "" || len(modelCode) > 128 {
		return nil, errInvalid("model_code 必填且不超过 128 字符", nil)
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if displayName == "" || len(displayName) > 128 {
		return nil, errInvalid("display_name 必填且不超过 128 字符", nil)
	}
	caps, err := parseCapabilities(in.Capabilities)
	if err != nil {
		return nil, err
	}
	params, err := parseDefaultParams(in.DefaultParams)
	if err != nil {
		return nil, err
	}
	contextSize := in.ContextSize
	if contextSize <= 0 {
		contextSize = defaultContextSize
	}
	var count int64
	if err := s.db.Model(&store.Model{}).
		Where("provider_id = ? AND model_code = ?", in.ProviderID, modelCode).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errConflict("该供应商下已存在相同 model_code 的模型", map[string]any{"model_code": modelCode})
	}
	m := store.Model{
		ID:            id.New(),
		ProviderID:    in.ProviderID,
		ModelCode:     modelCode,
		DisplayName:   displayName,
		Capabilities:  caps,
		ContextSize:   contextSize,
		DefaultParams: params,
		Status:        StatusActive,
	}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			ActorID: actorID,
			Action:  audit.ModelCreate,
			Target:  m.ID,
			Detail:  map[string]any{"provider_id": m.ProviderID, "model_code": m.ModelCode},
		})
	})
	if err != nil {
		return nil, err
	}
	var row modelRow
	if err := s.modelQuery().Where("model.id = ?", m.ID).Scan(&row).Error; err != nil {
		return nil, err
	}
	views := toModelViews([]modelRow{row})
	return &views[0], nil
}

type UpdateModelInput struct {
	ModelCode     *string
	DisplayName   *string
	Capabilities  json.RawMessage
	ContextSize   *int
	DefaultParams json.RawMessage
	Status        *string
}

func (s *Service) UpdateModel(actorID, modelID string, in UpdateModelInput) (*ModelView, error) {
	var m store.Model
	if err := s.db.First(&m, "id = ?", modelID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("模型不存在")
		}
		return nil, err
	}
	updates := map[string]any{}
	if in.ModelCode != nil {
		code := strings.TrimSpace(*in.ModelCode)
		if code == "" || len(code) > 128 {
			return nil, errInvalid("model_code 必填且不超过 128 字符", nil)
		}
		if code != m.ModelCode {
			var count int64
			if err := s.db.Model(&store.Model{}).
				Where("provider_id = ? AND model_code = ? AND id <> ?", m.ProviderID, code, m.ID).
				Count(&count).Error; err != nil {
				return nil, err
			}
			if count > 0 {
				return nil, errConflict("该供应商下已存在相同 model_code 的模型", map[string]any{"model_code": code})
			}
		}
		updates["model_code"] = code
	}
	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		if name == "" || len(name) > 128 {
			return nil, errInvalid("display_name 必填且不超过 128 字符", nil)
		}
		updates["display_name"] = name
	}
	if in.Capabilities != nil {
		caps, err := parseCapabilities(in.Capabilities)
		if err != nil {
			return nil, err
		}
		updates["capabilities"] = caps
	}
	if in.ContextSize != nil {
		if *in.ContextSize <= 0 {
			return nil, errInvalid("context_size 必须是正整数", map[string]any{"context_size": *in.ContextSize})
		}
		updates["context_size"] = *in.ContextSize
	}
	if in.DefaultParams != nil {
		params, err := parseDefaultParams(in.DefaultParams)
		if err != nil {
			return nil, err
		}
		updates["default_params"] = params
	}
	if in.Status != nil {
		if *in.Status != StatusActive && *in.Status != StatusDisabled {
			return nil, errInvalid("status 必须是 active 或 disabled", map[string]any{"status": *in.Status})
		}
		updates["status"] = *in.Status
	}
	if len(updates) > 0 {
		err := s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&m).Updates(updates).Error; err != nil {
				return err
			}
			return audit.Log(tx, audit.Entry{
				ActorID: actorID,
				Action:  audit.ModelUpdate,
				Target:  m.ID,
				Detail:  map[string]any{"model_code": m.ModelCode},
			})
		})
		if err != nil {
			return nil, err
		}
	}
	var row modelRow
	if err := s.modelQuery().Where("model.id = ?", m.ID).Scan(&row).Error; err != nil {
		return nil, err
	}
	views := toModelViews([]modelRow{row})
	return &views[0], nil
}

func (s *Service) DeleteModel(actorID, modelID string) error {
	var m store.Model
	if err := s.db.First(&m, "id = ?", modelID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errNotFound("模型不存在")
		}
		return err
	}
	var refCount int64
	if err := s.db.Model(&store.Agent{}).Where("model_id = ?", modelID).Count(&refCount).Error; err != nil {
		return err
	}
	if refCount > 0 {
		return errConflict("模型被智能体引用，无法删除", map[string]any{"agents": refCount})
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&m).Error; err != nil {
			return err
		}
		return audit.Log(tx, audit.Entry{
			ActorID: actorID,
			Action:  audit.ModelDelete,
			Target:  m.ID,
			Detail:  map[string]any{"provider_id": m.ProviderID, "model_code": m.ModelCode},
		})
	})
}

func (s *Service) ListSettings() ([]store.PlatformSetting, error) {
	settings := []store.PlatformSetting{}
	if err := s.db.Order("`key`").Find(&settings).Error; err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *Service) UpdateSetting(actorID, key string, value json.RawMessage) (*store.PlatformSetting, error) {
	if !ValidSettingKey(key) {
		return nil, errInvalid("未知的设置 key", map[string]any{"key": key})
	}
	if err := ValidateSettingValue(key, value); err != nil {
		return nil, err
	}
	var setting store.PlatformSetting
	err := s.db.Transaction(func(tx *gorm.DB) error {
		err := tx.First(&setting, "`key` = ?", key).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			setting = store.PlatformSetting{Key: key, Value: value, UpdatedBy: actorID}
			if err := tx.Create(&setting).Error; err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if err := tx.Model(&setting).Updates(map[string]any{
				"value":      value,
				"updated_by": actorID,
			}).Error; err != nil {
				return err
			}
		}
		return audit.Log(tx, audit.Entry{
			ActorID: actorID,
			Action:  audit.SettingsUpdate,
			Target:  key,
			Detail:  map[string]any{"value": json.RawMessage(value)},
		})
	})
	if err != nil {
		return nil, err
	}
	if err := s.db.First(&setting, "`key` = ?", key).Error; err != nil {
		return nil, err
	}
	return &setting, nil
}
