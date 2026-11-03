package subsonic

import (
	api "github.com/supersonic-app/go-subsonic/subsonic"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArtistArtworkPreservesCoverAndUsesOnlyGenuineImageURLs(t *testing.T) {
	for _, tt := range []struct{ cover, url, want string }{
		{"cover", "https://example.com/portrait", "cover"},
		{"", "https://example.com/portrait", artistImageURLPrefix + "https://example.com/portrait"},
		{"", "", ""}, {"", "file:///tmp/not-a-portrait", ""}, {"", "relative/path", ""},
	} {
		got := toArtistFromID3(&api.ArtistID3{CoverArt: tt.cover, ArtistImageUrl: tt.url})
		if got.CoverArtID != tt.want {
			t.Errorf("artwork %q/%q: %q", tt.cover, tt.url, got.CoverArtID)
		}
	}
}

func TestArtistImageURLFetchOmitsSubsonicCredentials(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/portrait.png" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected image request: %s", r.URL)
		}
		w.Header().Set("Content-Type", "image/png")
		png.Encode(w, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	}))
	defer server.Close()
	p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL, User: "private-user", UseJSON: true}}
	img, err := p.GetCoverArt(artistImageURLPrefix+server.URL+"/portrait.png", 300)
	if err != nil || img.Bounds().Dx() != 2 || calls != 1 {
		t.Fatalf("image=%v err=%v calls=%d", img, err, calls)
	}
	if _, err := p.GetCoverArt(artistImageURLPrefix+"file:///tmp/test", 0); err == nil {
		t.Fatal("accepted non-HTTP image URL")
	}
}
