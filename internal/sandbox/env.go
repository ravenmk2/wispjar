package sandbox

import (
	"fmt"
	"maps"
	"regexp"
	"strings"
)

// ReservedEnvKeys 由平台注入、不允许用户配置覆盖的环境变量名。
var ReservedEnvKeys = map[string]bool{"WISPJAR": true, "HOME": true}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	EnvMaxEntries    = 64
	EnvMaxValueBytes = 4096
)

// ValidateEnv 校验环境变量集合：合法键名、保留键不可配置、条数与值长度受限。
// 平台设置（sandbox.env）与智能体配置（sandbox_config.env）共用。
func ValidateEnv(env map[string]string) error {
	if len(env) > EnvMaxEntries {
		return fmt.Errorf("环境变量条目数不能超过 %d", EnvMaxEntries)
	}
	for k, v := range env {
		if !envKeyPattern.MatchString(k) {
			return fmt.Errorf("环境变量名 %q 必须匹配 [A-Za-z_][A-Za-z0-9_]*", k)
		}
		if ReservedEnvKeys[k] || strings.HasPrefix(strings.ToUpper(k), "EXECD_") {
			return fmt.Errorf("环境变量名 %s 为平台保留，不可配置", k)
		}
		if len(v) > EnvMaxValueBytes {
			return fmt.Errorf("环境变量 %s 的值过长（最大 %d 字节）", k, EnvMaxValueBytes)
		}
		if strings.ContainsRune(v, 0) {
			return fmt.Errorf("环境变量 %s 的值不能包含 NUL", k)
		}
	}
	return nil
}

// EnvPatch keeps omitted keys unchanged, including secrets the editor cannot read.
type EnvPatch struct {
	Set   map[string]string `json:"set"`
	Unset []string          `json:"unset"`
}

func (p EnvPatch) Apply(base map[string]string) (map[string]string, error) {
	if err := ValidateEnv(p.Set); err != nil {
		return nil, err
	}
	removed := make(map[string]string, len(p.Unset))
	for _, key := range p.Unset {
		if _, ok := p.Set[key]; ok {
			return nil, fmt.Errorf("环境变量 %s 不能同时设置和删除", key)
		}
		removed[key] = ""
	}
	if err := ValidateEnv(removed); err != nil {
		return nil, err
	}
	out := MergeEnv(base, p.Set)
	for key := range removed {
		delete(out, key)
	}
	return out, ValidateEnv(out)
}

// MergeEnv returns an independent map; later layers override earlier ones.
func MergeEnv(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, layer := range layers {
		maps.Copy(out, layer)
	}
	return out
}
