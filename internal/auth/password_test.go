package auth

import "testing"

func TestHashAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("secret123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPassword(hash, "secret123") {
		t.Fatal("CheckPassword: correct password rejected")
	}
	if CheckPassword(hash, "wrong-password") {
		t.Fatal("CheckPassword: wrong password accepted")
	}
	if CheckPassword("not-a-bcrypt-hash", "secret123") {
		t.Fatal("CheckPassword: malformed hash accepted")
	}
}

func TestValidateNewPassword(t *testing.T) {
	cases := []struct {
		password string
		want     bool
	}{
		{"abc12345", true},
		{"Abcdef12", true},
		{"short1", false},
		{"onlyletters", false},
		{"12345678", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := ValidateNewPassword(tc.password); got != tc.want {
			t.Errorf("ValidateNewPassword(%q) = %v, want %v", tc.password, got, tc.want)
		}
	}
}
