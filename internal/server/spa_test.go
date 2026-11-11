package server

import (
	"mime"
	"strings"
	"testing"
)

func TestWebmanifestMimeType(t *testing.T) {
	got := mime.TypeByExtension(".webmanifest")
	if !strings.HasPrefix(got, "application/manifest+json") {
		t.Fatalf("TypeByExtension(.webmanifest) = %q, want application/manifest+json", got)
	}
}
