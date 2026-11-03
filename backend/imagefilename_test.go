package backend

import (
	"strings"
	"testing"
)

func TestLongArtworkIDsHaveStableDistinctCacheNames(t *testing.T) {
	a := "supersonic-artist-image:https://example.com/" + strings.Repeat("a", 300)
	b := a + "b"
	got := sanitizeFileName(a)
	if len(got) > 200 || got != sanitizeFileName(a) || got == sanitizeFileName(b) {
		t.Fatal("invalid long image cache key")
	}
	if sanitizeFileName("https://example.com/a?b") == sanitizeFileName("https://example.com/a/b") {
		t.Fatal("distinct image URLs collided")
	}
	if sanitizeFileName("existing-id") != "existing-id" {
		t.Fatal("existing cache key changed")
	}
}
