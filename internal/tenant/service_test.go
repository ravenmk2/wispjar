package tenant

import (
	"testing"

	"github.com/ravenmk2/wispjar/internal/auth"
)

func TestRemovingLastAdmin(t *testing.T) {
	cases := []struct {
		role       string
		adminCount int64
		want       bool
	}{
		{RoleAdmin, 1, true},
		{RoleAdmin, 0, true},
		{RoleAdmin, 2, false},
		{RoleMember, 1, false},
		{RoleMember, 0, false},
	}
	for _, tc := range cases {
		if got := removingLastAdmin(tc.role, tc.adminCount); got != tc.want {
			t.Errorf("removingLastAdmin(%q, %d) = %v, want %v", tc.role, tc.adminCount, got, tc.want)
		}
	}
}

func TestValidRole(t *testing.T) {
	for _, r := range []string{RoleAdmin, RoleMember} {
		if !validRole(r) {
			t.Errorf("validRole(%q) = false, want true", r)
		}
	}
	for _, r := range []string{"", "owner", "Admin", "superadmin"} {
		if validRole(r) {
			t.Errorf("validRole(%q) = true, want false", r)
		}
	}
}

func TestValidStatus(t *testing.T) {
	for _, s := range []string{StatusActive, StatusDisabled} {
		if !validStatus(s) {
			t.Errorf("validStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "archived", "Active"} {
		if validStatus(s) {
			t.Errorf("validStatus(%q) = true, want false", s)
		}
	}
}

func TestRandomPassword(t *testing.T) {
	for i := 0; i < 100; i++ {
		pw, err := randomPassword(12)
		if err != nil {
			t.Fatalf("randomPassword: %v", err)
		}
		if len(pw) != 12 {
			t.Errorf("len = %d, want 12", len(pw))
		}
		if !auth.ValidateNewPassword(pw) {
			t.Errorf("password %q does not satisfy policy", pw)
		}
	}
}
