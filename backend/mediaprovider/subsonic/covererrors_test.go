package subsonic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/supersonic-app/go-subsonic/subsonic"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

func TestStructuredArtworkErrors(t *testing.T) {
	for _, tt := range []struct {
		name         string
		status, code int
		reason, mime string
		quiet, retry bool
	}{
		{"missing XML", 200, 70, "", "application/xml", true, false},
		{"missing JSON", 200, 70, "", "application/json", true, false},
		{"auth XML", 200, 40, "", "application/xml", false, false},
		{"auth cannot be hidden", 503, 40, "release-pending", "application/xml", false, false},
		{"pending", 503, 0, "release-pending", "application/xml", true, true},
		{"busy", 503, 0, "release-busy", "application/xml", true, true},
		{"absent release", 404, 0, "release-unavailable", "application/xml", true, false},
		{"unexpected failure", 503, 0, "release-failed", "application/xml", false, true},
		{"unsupported endpoint", 200, 0, "", "application/xml", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := fmt.Sprintf(`<subsonic-response xmlns="http://subsonic.org/restapi" status="failed" version="1.16.1"><error code="%d" message="fixture API error"/></subsonic-response>`, tt.code)
			if tt.mime == "application/json" {
				body = fmt.Sprintf(`{"subsonic-response":{"status":"failed","error":{"code":%d,"message":"fixture API error"}}}`, tt.code)
			}
			resp := &http.Response{StatusCode: tt.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
			resp.Header.Set("Content-Type", tt.mime)
			resp.Header.Set("X-Artwork-Status", "unavailable")
			resp.Header.Set("X-Artwork-Reason", tt.reason)
			img, retry, err := decodeCoverResponse(resp)
			if img != nil || err == nil || retry != tt.retry || mediaprovider.IsExpectedArtworkError(err) != tt.quiet {
				t.Fatalf("image=%v retry=%v error=%v quiet=%v", img, retry, err, mediaprovider.IsExpectedArtworkError(err))
			}
			if strings.Contains(err.Error(), "unknown format") {
				t.Fatal("API error treated as image bytes")
			}
			if !tt.quiet && !strings.Contains(err.Error(), "fixture API error") {
				t.Fatal("API message lost")
			}
		})
	}
}

func TestArtworkCooldownSuppressesRepeatRequestsAndExpires(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/xml")
		w.Header().Set("X-Artwork-Status", "unavailable")
		w.Header().Set("X-Artwork-Reason", "release-busy")
		w.Header().Set("Retry-After", "50")
		w.WriteHeader(503)
		fmt.Fprint(w, `<subsonic-response status="failed"><error code="0" message="Artwork temporarily unavailable [release-busy]; retry later"/></subsonic-response>`)
	}))
	defer server.Close()
	p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL}}
	for n := 0; n < 3; n++ {
		_, err := p.GetCoverArtContext(context.Background(), "artist-image:1", 300)
		if !mediaprovider.IsExpectedArtworkError(err) {
			t.Fatalf("unexpected error: %v", err)
		}
		var retry *mediaprovider.ArtworkRetryError
		if !errors.As(err, &retry) || time.Until(retry.RetryAt) < 49*time.Second {
			t.Fatalf("lost retry deadline: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("cooldown made %d HTTP calls", calls)
	}
	p.coverCooldownMu.Lock()
	entry := p.coverCooldowns[coverCooldownKey{"artist-image:1", 300}]
	entry.until = time.Now().Add(-time.Second)
	p.coverCooldowns[coverCooldownKey{"artist-image:1", 300}] = entry
	p.coverCooldownMu.Unlock()
	p.GetCoverArtContext(context.Background(), "artist-image:1", 300)
	if calls != 2 {
		t.Fatal("expired cooldown did not permit recovery")
	}
	t.Log("three viewport loads during 50s cooldown: 1 HTTP request; expired cooldown permits next request")
}

func TestArtistArtworkNamespaces(t *testing.T) {
	p := &subsonicMediaProvider{}
	for _, id := range []string{"artist-image:1", "artist-release:am:br:lossless:artist:1", artistImageURLPrefix + "https://example.com/artist.jpg"} {
		if !p.IsArtistCoverArt(id) {
			t.Errorf("artist artwork not limited: %s", id)
		}
	}
	if p.IsArtistCoverArt("am:br:lossless:album:1") {
		t.Fatal("album incorrectly uses artist limit")
	}
}
