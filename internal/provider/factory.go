package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"gorm.io/gorm"

	"github.com/ravenmk2/wispjar/internal/store"
)

// ChatModelFactory 按 model_id 构造 eino ToolCallingChatModel。
// v1 所有供应商类型（deepseek/ark/qwen/openai_compat）统一走 openai 兼容组件；
// 密钥解密仅发生在工厂内部，不明文出包。
type ChatModelFactory struct {
	db        *gorm.DB
	masterKey []byte
	cache     sync.Map // key: modelID+"|"+paramsHash -> model.ToolCallingChatModel
	media     *mediaStore
}

func NewChatModelFactory(db *gorm.DB, masterKey []byte, readers ...MediaReader) *ChatModelFactory {
	media := &mediaStore{db: db}
	if len(readers) > 0 {
		media.reader = readers[0]
	}
	return &ChatModelFactory{db: db, masterKey: masterKey, media: media}
}

func (f *ChatModelFactory) NewChatModel(ctx context.Context, modelID string, params map[string]any) (model.ToolCallingChatModel, error) {
	var row struct {
		store.Model
		ProviderType string `gorm:"column:provider_type"`
		BaseURL      string `gorm:"column:base_url"`
		APIKeyEnc    []byte `gorm:"column:api_key_enc"`
	}
	err := f.db.WithContext(ctx).
		Model(&store.Model{}).
		Select("model.*, model_provider.type AS provider_type, model_provider.base_url, model_provider.api_key_enc").
		Joins("JOIN model_provider ON model_provider.id = model.provider_id").
		Where("model.id = ?", modelID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound("模型不存在")
		}
		return nil, err
	}

	merged := mergeParams(row.DefaultParams, params)
	key := modelID + "|" + paramsHash(map[string]any{"params": merged, "endpoint": row.BaseURL, "key": row.APIKeyEnc, "caps": row.Capabilities, "model": row.ModelCode, "provider": row.ProviderType})
	if v, ok := f.cache.Load(key); ok {
		return v.(model.ToolCallingChatModel), nil
	}

	apiKey, err := Decrypt(f.masterKey, row.APIKeyEnc)
	if err != nil {
		return nil, err
	}
	var caps Capabilities
	if err := json.Unmarshal(row.Capabilities, &caps); err != nil {
		return nil, err
	}
	if caps.APIProtocol == "responses" {
		if row.ProviderType != "deepseek" && row.ProviderType != "ark" && row.ProviderType != "openai_compat" {
			return nil, fmt.Errorf("provider: Responses adapter unavailable for %s", row.ProviderType)
		}
		cm := &responsesModel{baseURL: row.BaseURL, apiKey: apiKey, providerID: row.ProviderID, providerType: row.ProviderType, modelCode: row.ModelCode, params: merged, caps: caps, media: f.media, client: mediaHTTPClient()}
		actual, _ := f.cache.LoadOrStore(key, model.ToolCallingChatModel(cm))
		return actual.(model.ToolCallingChatModel), nil
	}
	adapter := AdapterFor(row.ProviderType)
	cfg := &openai.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: row.BaseURL,
		Model:   row.ModelCode,
		// 旁路抓取原始 usage（eino acl 丢弃供应商特有缓存字段）；
		// 设置 HTTPClient 后组件不再用 Timeout（组件默认亦无超时，行为不变）。
		HTTPClient: &http.Client{Transport: newUsageCaptureTransport(nil, adapter)},
	}
	adapter.ApplyParams(cfg, merged)
	cm, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("provider: 构建模型实例失败: %w", err)
	}
	wrapped := &inlineMediaModel{ToolCallingChatModel: cm, media: f.media}
	actual, _ := f.cache.LoadOrStore(key, model.ToolCallingChatModel(wrapped))
	return actual.(model.ToolCallingChatModel), nil
}

// 参数映射已收敛进适配器注册表（adapters.go，runtime.md §3）：
// temperature/top_p/max_tokens 通用；平台 reasoning_effort 在 reasoning.go
// 按供应商/模型转换；prompt_cache_key 仅 ark 放行。

func setExtraField(cfg *openai.ChatModelConfig, key string, val any) {
	if cfg.ExtraFields == nil {
		cfg.ExtraFields = map[string]any{}
	}
	cfg.ExtraFields[key] = val
}

func stringParam(params map[string]any, name string) (string, bool) {
	v, ok := params[name]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok && s != ""
}

// mergeParams 合并模型默认参数与会话级覆盖参数。
// defaultParams 可能是 JSON 字面量 null（会解出 nil map），也可能为空或非法，一律回退为空表。
func mergeParams(defaultParams json.RawMessage, params map[string]any) map[string]any {
	merged := map[string]any{}
	if len(defaultParams) > 0 {
		if err := json.Unmarshal(defaultParams, &merged); err != nil || merged == nil {
			merged = map[string]any{}
		}
	}
	for k, v := range params {
		merged[k] = v
	}
	return merged
}

func numberParam(params map[string]any, name string) (float64, bool) {
	v, ok := params[name]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func paramsHash(params map[string]any) string {
	b, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
