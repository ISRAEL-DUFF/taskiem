package main

import "testing"

func TestLoadPublicConfig(t *testing.T) {
	// Only its own login: no TASKIEM_DATABASE_URL, no KMS, nothing else.
	t.Setenv("TASKIEM_DATABASE_URL", "")
	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", "postgres://pub@db/taskiem")
	c, err := loadPublicConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Public.DSN != "postgres://pub@db/taskiem" || c.Public.Role != "taskiem_public" || c.Public.PoolSize != 5 || c.Public.Listen != ":8082" {
		t.Fatalf("%+v", c.Public)
	}

	t.Setenv("TASKIEM_PUBLIC_DATABASE_URL", "")
	if _, err := loadPublicConfig(); err == nil {
		t.Fatal("no database at all must be refused")
	}
	// Without its own, the public host falls back to the platform's DSN.
	t.Setenv("TASKIEM_DATABASE_URL", "postgres://app@db/taskiem")
	if c, err := loadPublicConfig(); err != nil || c.Public.DSN != "postgres://app@db/taskiem" {
		t.Fatalf("%v %+v", err, c.Public)
	}

	for _, v := range []string{"0", "101", "lots"} {
		t.Setenv("TASKIEM_PUBLIC_DATABASE_POOL", v)
		if _, err := loadPublicConfig(); err == nil {
			t.Errorf("pool %q accepted", v)
		}
	}
}

func TestSameLogin(t *testing.T) {
	if !sameLogin("postgres://app:x@db:5432/taskiem", "postgres://app:y@db:5432/taskiem") {
		t.Error("same user, host and database")
	}
	if sameLogin("postgres://pub@db/taskiem", "postgres://app@db/taskiem") {
		t.Error("different users")
	}
}
