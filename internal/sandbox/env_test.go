package sandbox

import (
	"strings"
	"testing"
)

func TestValidateEnvOK(t *testing.T) {
	if err := ValidateEnv(nil); err != nil {
		t.Fatalf("nil env must pass: %v", err)
	}
	if err := ValidateEnv(map[string]string{"FOO_BAR": "v", "_X": "", "a1": "2"}); err != nil {
		t.Fatalf("valid env must pass: %v", err)
	}
}

func TestValidateEnvBadKey(t *testing.T) {
	for _, k := range []string{"", "1ABC", "A-B", "A B", "中文"} {
		if err := ValidateEnv(map[string]string{k: "v"}); err == nil {
			t.Fatalf("key %q must be rejected", k)
		}
	}
}

func TestValidateEnvReservedKey(t *testing.T) {
	err := ValidateEnv(map[string]string{"WISPJAR": "x"})
	if err == nil || !strings.Contains(err.Error(), "保留") {
		t.Fatalf("reserved key must be rejected, got %v", err)
	}
}

func TestValidateEnvLimits(t *testing.T) {
	tooMany := map[string]string{}
	for i := 0; i < EnvMaxEntries+1; i++ {
		tooMany[string(rune('A'+i%26))+strings.Repeat("A", i/26)] = "v"
	}
	if err := ValidateEnv(tooMany); err == nil {
		t.Fatal("entry count above limit must be rejected")
	}
	if err := ValidateEnv(map[string]string{"BIG": strings.Repeat("x", EnvMaxValueBytes+1)}); err == nil {
		t.Fatal("oversized value must be rejected")
	}
}

func TestMergeEnvReservedKeyWins(t *testing.T) {
	got := mergeEnv(map[string]string{"FOO": "bar", "WISPJAR": "0"})
	if got["FOO"] != "bar" {
		t.Fatalf("user env lost: %+v", got)
	}
	if got["WISPJAR"] != "1" {
		t.Fatalf("reserved key must not be overridable: %+v", got)
	}
	if got := mergeEnv(nil); got["WISPJAR"] != "1" || len(got) != 1 {
		t.Fatalf("nil env must still carry marker: %+v", got)
	}
}
