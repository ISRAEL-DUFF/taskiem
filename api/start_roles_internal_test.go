package api

import (
	"testing"

	"github.com/google/uuid"
)

func TestMayStart(t *testing.T) {
	roles := []string{"operator", "shift_manager"}
	for _, c := range []struct {
		name string
		p    *Principal
		want bool
	}{
		{"no roles named", &Principal{Roles: []string{"builder"}}, true},
		{"a role named", &Principal{Roles: []string{"shift_manager"}}, true},
		{"an owner", &Principal{Roles: []string{"owner"}}, true},
		{"another role", &Principal{Roles: []string{"builder"}}, false},
		{"a key of an operator", &Principal{KeyID: uuid.New(), KeyOwnerRoles: []string{"operator"}}, true},
		{"a key of a builder", &Principal{KeyID: uuid.New(), KeyOwnerRoles: []string{"builder"}}, false},
		{"an end user", &Principal{EndUser: &EndUser{}}, false},
		{"nobody", nil, false},
	} {
		r := roles
		if c.name == "no roles named" {
			r = nil
		}
		if got := mayStart(c.p, r); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if got := startRolesWords([]string{"a", "b", "c"}); got != "a, b or c" {
		t.Errorf("words: %q", got)
	}
}
