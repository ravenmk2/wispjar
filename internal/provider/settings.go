package provider

import (
	"encoding/json"
	"strings"

	"github.com/ravenmk2/wispjar/internal/sandbox"
)

const (
	SettingDefaultImage   = "sandbox.default_image"
	SettingImages         = "sandbox.images"
	SettingEgressPolicy   = "sandbox.egress_policy"
	SettingResourceLimits = "sandbox.resource_limits"
	SettingEnv            = "sandbox.env"
	SettingIdleTTLSeconds = "sandbox.idle_ttl_seconds"
	SettingTTLSeconds     = "sandbox.ttl_seconds"
)

func ValidSettingKey(key string) bool {
	switch key {
	case SettingDefaultImage, SettingImages, SettingEgressPolicy,
		SettingResourceLimits, SettingEnv, SettingIdleTTLSeconds, SettingTTLSeconds:
		return true
	}
	return false
}

func ValidateSettingValue(key string, value json.RawMessage) error {
	switch key {
	case SettingDefaultImage:
		var s string
		if err := json.Unmarshal(value, &s); err != nil || strings.TrimSpace(s) == "" {
			return errInvalid("sandbox.default_image 必须是非空字符串", nil)
		}
	case SettingImages:
		var arr []string
		if err := json.Unmarshal(value, &arr); err != nil {
			return errInvalid("sandbox.images 必须是字符串数组", nil)
		}
		for _, s := range arr {
			if strings.TrimSpace(s) == "" {
				return errInvalid("sandbox.images 不允许空镜像名", nil)
			}
		}
	case SettingEgressPolicy:
		var p struct {
			DefaultAction string `json:"defaultAction"`
			Rules         []struct {
				Action string `json:"action"`
				Target string `json:"target"`
			} `json:"rules"`
		}
		if err := json.Unmarshal(value, &p); err != nil {
			return errInvalid("sandbox.egress_policy 必须是对象 {defaultAction, rules}", nil)
		}
		if p.DefaultAction != "allow" && p.DefaultAction != "deny" {
			return errInvalid("defaultAction 必须是 allow 或 deny", map[string]any{"defaultAction": p.DefaultAction})
		}
		for _, r := range p.Rules {
			if r.Action != "allow" && r.Action != "deny" {
				return errInvalid("rules[].action 必须是 allow 或 deny", map[string]any{"action": r.Action})
			}
			if strings.TrimSpace(r.Target) == "" {
				return errInvalid("rules[].target 不允许为空", nil)
			}
		}
	case SettingResourceLimits:
		var r struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		}
		if err := json.Unmarshal(value, &r); err != nil || strings.TrimSpace(r.CPU) == "" || strings.TrimSpace(r.Memory) == "" {
			return errInvalid("sandbox.resource_limits 必须是 {cpu, memory} 非空字符串", nil)
		}
	case SettingEnv:
		var env map[string]string
		if err := json.Unmarshal(value, &env); err != nil {
			return errInvalid("sandbox.env 必须是 {KEY: \"value\"} 对象", nil)
		}
		if err := sandbox.ValidateEnv(env); err != nil {
			return errInvalid(err.Error(), nil)
		}
	case SettingIdleTTLSeconds, SettingTTLSeconds:
		var n float64
		if err := json.Unmarshal(value, &n); err != nil {
			return errInvalid(key+" 必须是数字（秒）", nil)
		}
		if n < 0 || n != float64(int64(n)) {
			return errInvalid(key+" 必须是非负整数（秒）", map[string]any{"value": n})
		}
	default:
		return errInvalid("未知的设置 key", map[string]any{"key": key})
	}
	return nil
}
