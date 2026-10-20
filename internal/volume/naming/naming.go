// Package naming defines backend-independent persistent volume naming rules.
package naming

import (
	"fmt"
	"regexp"
	"strings"
)

const DefaultPattern = "wj-{namespace}-{kind}-{volume_id}"

var validName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func Render(pattern, namespace, kind, volumeID, orgID, userID string) (string, error) {
	if !strings.Contains(pattern, "{volume_id}") {
		return "", fmt.Errorf("volume name pattern must contain {volume_id}")
	}
	values := map[string]string{"namespace": namespace, "kind": kind, "volume_id": volumeID, "org_id": orgID, "user_id": userID}
	var out strings.Builder
	for len(pattern) > 0 {
		i := strings.IndexByte(pattern, '{')
		if i < 0 {
			out.WriteString(pattern)
			break
		}
		out.WriteString(pattern[:i])
		j := strings.IndexByte(pattern[i:], '}')
		if j < 0 {
			return "", fmt.Errorf("unclosed volume name placeholder")
		}
		key := pattern[i+1 : i+j]
		value, ok := values[key]
		if !ok {
			return "", fmt.Errorf("unknown volume name placeholder %q", key)
		}
		out.WriteString(value)
		pattern = pattern[i+j+1:]
	}
	name := out.String()
	if len(name) > 63 || !validName.MatchString(name) {
		return "", fmt.Errorf("volume name must match lowercase DNS label syntax and be at most 63 characters")
	}
	return name, nil
}

func Validate(pattern, namespace string) error {
	// Validate against actual ID lengths and the longest supported kind.
	_, err := Render(pattern, namespace, "sessions", strings.Repeat("0", 32), strings.Repeat("0", 32), strings.Repeat("0", 32))
	return err
}
