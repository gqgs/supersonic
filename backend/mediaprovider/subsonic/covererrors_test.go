package subsonic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
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
		{"provider failure", 503, 0, "release-failed", "application/xml", true, true},
		{"catalog cooldown", 503, 0, "release-deferred", "application/xml", true, true},
		{"unexpected failure", 503, 0, "unknown", "application/xml", false, true},
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

func TestDeferredAndFailedArtworkKeepInitialAndCachedDeadlines(t *testing.T) {
	for _, reason := range []string{"release-deferred", "release-failed"} {
		for _, seconds := range []int{60, 300, 3600} {
			for _, dateHeader := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/date=%v", reason, seconds, dateHeader), func(t *testing.T) {
					var calls atomic.Int32
					deadline := time.Now().Add(time.Duration(seconds) * time.Second).Truncate(time.Second)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						w.Header().Set("X-Artwork-Status", "unavailable")
						w.Header().Set("X-Artwork-Reason", reason)
						if dateHeader {
							w.Header().Set("Retry-After", deadline.UTC().Format(http.TimeFormat))
						} else {
							w.Header().Set("Retry-After", fmt.Sprint(seconds))
						}
						w.WriteHeader(503)
						fmt.Fprint(w, `<subsonic-response status="failed"><error code="0" message="temporary"/></subsonic-response>`)
					}))
					defer server.Close()
					p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL}}
					var first time.Time
					for n := 0; n < 2; n++ {
						_, err := p.GetCoverArt("artist-release:am:br:lossless:artist:1", 300)
						var retry *mediaprovider.ArtworkRetryError
						if !errors.Is(err, mediaprovider.ErrArtworkPending) || !errors.As(err, &retry) {
							t.Fatalf("missing pending deadline: %v", err)
						}
						if retry.RetryAt.Before(deadline) || retry.RetryAt.After(deadline.Add(2*time.Second)) {
							t.Fatalf("incorrect deadline %v, expected %v", retry.RetryAt, deadline)
						}
						if n == 0 {
							first = retry.RetryAt
						} else if !first.Equal(retry.RetryAt) {
							t.Fatal("cache extended deadline")
						}
					}
					if calls.Load() != 1 {
						t.Fatalf("made %d requests during cooldown", calls.Load())
					}
				})
			}
		}
	}
}

// This fixture is shared with apple-music-bridge's HTTP producer test.
func TestBridgeRetryProtocolFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/artwork_retry_protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Reason          string
		Seconds, Status int
		Pending         bool
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		resp := &http.Response{StatusCode: tc.Status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<subsonic-response status="failed"><error code="0" message="Artwork unavailable"/></subsonic-response>`))}
		resp.Header.Set("X-Artwork-Status", "unavailable")
		resp.Header.Set("X-Artwork-Reason", tc.Reason)
		resp.Header.Set("Retry-After", fmt.Sprint(tc.Seconds))
		_, retry, err := decodeCoverResponse(resp)
		if retry != tc.Pending || errors.Is(err, mediaprovider.ErrArtworkPending) != tc.Pending || !mediaprovider.IsExpectedArtworkError(err) {
			t.Fatalf("%+v: retry=%v err=%v", tc, retry, err)
		}
		if coverRetryAfter(resp) != time.Duration(tc.Seconds)*time.Second {
			t.Fatalf("lost delay for %+v", tc)
		}
	}
}
