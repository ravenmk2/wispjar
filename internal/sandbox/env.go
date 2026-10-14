package sandbox

import (
	"fmt"
	"regexp"
)

// ReservedEnvKeys 由平台注入、不允许用户配置覆盖的环境变量名。
var ReservedEnvKeys = map[string]bool{"WISPJAR": true}

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
		if ReservedEnvKeys[k] {
			return fmt.Errorf("环境变量名 %s 为平台保留，不可配置", k)
		}
		if len(v) > EnvMaxValueBytes {
			return fmt.Errorf("环境变量 %s 的值过长（最大 %d 字节）", k, EnvMaxValueBytes)
		}
	}
	return nil
}
