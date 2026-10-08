package remote

import (
	"testing"
)

func TestValidateHost(t *testing.T) {
	validHosts := []string{
		"localhost",
		"127.0.0.1",
		"server-1.example.com",
		"sub.dom.co.uk",
	}

	for _, h := range validHosts {
		if err := ValidateHost(h); err != nil {
			t.Errorf("ValidateHost(%q) returned unexpected error: %v", h, err)
		}
	}

	invalidHosts := []string{
		"",
		"host;evil",
		"host evil",
		"host$(cmd)",
		"host`cmd`",
		"host|cmd",
	}

	for _, h := range invalidHosts {
		if err := ValidateHost(h); err == nil {
			t.Errorf("ValidateHost(%q) expected error, got nil", h)
		}
	}
}

func TestValidateUser(t *testing.T) {
	validUsers := []string{
		"root",
		"admin_user",
		"user-123",
		"john.doe",
	}

	for _, u := range validUsers {
		if err := ValidateUser(u); err != nil {
			t.Errorf("ValidateUser(%q) returned unexpected error: %v", u, err)
		}
	}

	invalidUsers := []string{
		"",
		"user;evil",
		"user evil",
		"user$(cmd)",
		"user`cmd`",
	}

	for _, u := range invalidUsers {
		if err := ValidateUser(u); err == nil {
			t.Errorf("ValidateUser(%q) expected error, got nil", u)
		}
	}
}
