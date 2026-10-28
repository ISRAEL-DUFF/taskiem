package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/israel-duff/taskiem/engine/archive"
	"github.com/israel-duff/taskiem/engine/runtime"
)

func TestArchiverFromEnv(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	from := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	creds := map[string]string{"TASKIEM_ARCHIVE_S3_BUCKET": "runs", "TASKIEM_ARCHIVE_S3_ACCESS_KEY_ID": "id", "TASKIEM_ARCHIVE_S3_SECRET_ACCESS_KEY": "secret"}
	with := func(more map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range creds {
			m[k] = v
		}
		for k, v := range more {
			m[k] = v
		}
		return m
	}

	if a, err := archiver(from(nil), log); a != nil || err != nil {
		t.Errorf("nothing set: %v %v", a, err)
	}
	if a, err := archiver(from(map[string]string{"TASKIEM_ARCHIVE_DIR": "/var/lib/taskiem/archive"}), log); err != nil || a != (runtime.FileArchiver{Dir: "/var/lib/taskiem/archive"}) {
		t.Errorf("directory: %v %v", a, err)
	}

	// A bucket on AWS: the region's endpoint, virtual-hosted, runs/.
	a, err := archiver(from(with(map[string]string{"TASKIEM_ARCHIVE_S3_REGION": "af-south-1", "TASKIEM_ARCHIVE_DIR": "/ignored"})), log)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := a.(*archive.S3)
	if !ok || s.Endpoint.String() != "https://s3.af-south-1.amazonaws.com" || s.PathStyle || s.Prefix != "runs/" || s.Signer.Region != "af-south-1" {
		t.Errorf("aws: %#v", a)
	}
	// Self-hosted: its endpoint, path-style, the default region.
	a, err = archiver(from(with(map[string]string{"TASKIEM_ARCHIVE_S3_ENDPOINT": "http://seaweedfs:8333/", "TASKIEM_ARCHIVE_S3_PATH_STYLE": "true"})), log)
	if s, ok := a.(*archive.S3); err != nil || !ok || s.Endpoint.String() != "http://seaweedfs:8333" || !s.PathStyle || s.Signer.Region != "us-east-1" {
		t.Errorf("self-hosted: %#v %v", a, err)
	}

	for name, vars := range map[string]map[string]string{
		"credentials without a bucket": {"TASKIEM_ARCHIVE_S3_ACCESS_KEY_ID": "id", "TASKIEM_ARCHIVE_DIR": "/d"},
		"no secret":                    {"TASKIEM_ARCHIVE_S3_BUCKET": "runs", "TASKIEM_ARCHIVE_S3_ACCESS_KEY_ID": "id"},
		"path style not a boolean":     with(map[string]string{"TASKIEM_ARCHIVE_S3_PATH_STYLE": "maybe"}),
		"unknown encryption":           with(map[string]string{"TASKIEM_ARCHIVE_S3_SSE": "rot13"}),
	} {
		if a, err := archiver(from(vars), log); err == nil || !strings.Contains(err.Error(), "TASKIEM_ARCHIVE_") {
			t.Errorf("%s: %v %v", name, a, err)
		}
	}
}
