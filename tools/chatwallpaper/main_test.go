package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWallpaperIsCurrent(t *testing.T) {
	files, err := Generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range files {
		got, err := os.ReadFile(filepath.Join("../..", path))
		if err != nil {
			t.Fatalf("%s: %v (run go run ./tools/chatwallpaper)", path, err)
		}
		if string(got) != want {
			t.Errorf("%s is stale: run go run ./tools/chatwallpaper", path)
		}
	}
}

func TestPackedClose(t *testing.T) {
	n := strings.Count(Tile(), "<path ")
	if n < 200 {
		t.Errorf("%d doodles drawn on the tile; packed close, as WhatsApp's, it holds more than 200", n)
	}
	names := map[string]bool{}
	for _, d := range Doodles {
		if names[d.Name] {
			t.Errorf("%s drawn twice", d.Name)
		}
		names[d.Name] = true
	}
}
