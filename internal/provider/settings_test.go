package provider

import (
	"encoding/json"
	"testing"
)

func TestValidSettingKey(t *testing.T) {
	for _, key := range []string{
		SettingDefaultImage, SettingImages, SettingEgressPolicy,
		SettingResourceLimits, SettingEnv, SettingIdleTTLSeconds, SettingTTLSeconds,
	} {
		if !ValidSettingKey(key) {
			t.Errorf("ValidSettingKey(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"", "sandbox.unknown", "SANDBOX.IMAGES", "auth.jwt_secret"} {
		if ValidSettingKey(key) {
			t.Errorf("ValidSettingKey(%q) = true, want false", key)
		}
	}
}

func TestValidateSettingValue(t *testing.T) {
	cases := []struct {
		key   string
		value string
		ok    bool
	}{
		{SettingDefaultImage, `"python:3.12"`, true},
		{SettingDefaultImage, `""`, false},
		{SettingDefaultImage, `123`, false},
		{SettingDefaultImage, `["python:3.12"]`, false},

		{SettingImages, `["python:3.12","node:20"]`, true},
		{SettingImages, `[]`, true},
		{SettingImages, `[""]`, false},
		{SettingImages, `["python:3.12", 1]`, false},
		{SettingImages, `"python:3.12"`, false},

		{SettingEgressPolicy, `{"defaultAction":"allow","rules":[]}`, true},
		{SettingEgressPolicy, `{"defaultAction":"deny","rules":[{"action":"allow","target":"pypi.org"}]}`, true},
		{SettingEgressPolicy, `{"defaultAction":"block","rules":[]}`, false},
		{SettingEgressPolicy, `{"defaultAction":"allow","rules":[{"action":"block","target":"x"}]}`, false},
		{SettingEgressPolicy, `{"defaultAction":"allow","rules":[{"action":"deny","target":""}]}`, false},
		{SettingEgressPolicy, `"allow"`, false},

		{SettingResourceLimits, `{"cpu":"1","memory":"1Gi"}`, true},
		{SettingResourceLimits, `{"cpu":"","memory":"1Gi"}`, false},
		{SettingResourceLimits, `{"cpu":"1"}`, false},
		{SettingResourceLimits, `{"cpu":1,"memory":"1Gi"}`, false},

		{SettingIdleTTLSeconds, `1800`, true},
		{SettingIdleTTLSeconds, `0`, true},
		{SettingIdleTTLSeconds, `-1`, false},
		{SettingIdleTTLSeconds, `1.5`, false},
		{SettingIdleTTLSeconds, `"1800"`, false},

		{SettingTTLSeconds, `3600`, true},
		{SettingTTLSeconds, `-3600`, false},

		{SettingEnv, `{}`, true},
		{SettingEnv, `{"FOO_BAR":"baz","_X":""}`, true},
		{SettingEnv, `{"1BAD":"v"}`, false},
		{SettingEnv, `{"A-B":"v"}`, false},
		{SettingEnv, `{"WISPJAR":"0"}`, false},
		{SettingEnv, `["FOO"]`, false},
		{SettingEnv, `{"FOO":1}`, false},

		{"sandbox.unknown", `1`, false},
	}
	for _, tc := range cases {
		err := ValidateSettingValue(tc.key, json.RawMessage(tc.value))
		if tc.ok && err != nil {
			t.Errorf("ValidateSettingValue(%q, %s) = %v, want nil", tc.key, tc.value, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("ValidateSettingValue(%q, %s) = nil, want error", tc.key, tc.value)
		}
	}
}
