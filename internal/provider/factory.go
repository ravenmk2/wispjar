package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
}

func NewChatModelFactory(db *gorm.DB, masterKey []byte) *ChatModelFactory {
	return &ChatModelFactory{db: db, masterKey: masterKey}
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
	key := modelID + "|" + paramsHash(merged)
	if v, ok := f.cache.Load(key); ok {
		return v.(model.ToolCallingChatModel), nil
	}

	apiKey, err := Decrypt(f.masterKey, row.APIKeyEnc)
	if err != nil {
		return nil, err
	}
	cfg := &openai.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: row.BaseURL,
		Model:   row.ModelCode,
	}
	if v, ok := numberParam(merged, "temperature"); ok {
		f32 := float32(v)
		cfg.Temperature = &f32
	}
	if v, ok := numberParam(merged, "top_p"); ok {
		f32 := float32(v)
		cfg.TopP = &f32
	}
	if v, ok := numberParam(merged, "max_tokens"); ok {
		n := int(v)
		cfg.MaxTokens = &n
	}
	cm, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("provider: 构建模型实例失败: %w", err)
	}
	actual, _ := f.cache.LoadOrStore(key, model.ToolCallingChatModel(cm))
	return actual.(model.ToolCallingChatModel), nil
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
