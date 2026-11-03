package subsonic

import (
	"fmt"
	api "github.com/supersonic-app/go-subsonic/subsonic"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCoverArtTransientResponses(t *testing.T) {
	for _, tt := range []struct {
		name        string
		status      int
		contentType string
		placeholder bool
		succeeds    bool
		calls       int
		wantError   string
	}{
		{name: "pending then ready", status: 503, succeeds: true, calls: 2},
		{name: "accepted then ready", status: 202, succeeds: true, calls: 2},
		{name: "retry bounded", status: 503, calls: 4, wantError: "HTTP 503"},
		{name: "missing", status: 404, calls: 1, wantError: "HTTP 404"},
		{name: "JSON error", status: 200, contentType: "application/json", calls: 1, wantError: "non-image"},
		{name: "XML error", status: 200, contentType: "application/xml", calls: 1, wantError: "non-image"},
		{name: "temporary placeholder", status: 200, placeholder: true, calls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Query().Get("id") != "artist-1" || r.URL.Query().Get("size") != "300" {
					t.Errorf("missing request parameters")
				}
				if tt.succeeds && calls > 1 {
					png.Encode(w, image.NewRGBA(image.Rect(0, 0, 2, 2)))
					return
				}
				if tt.placeholder {
					w.Header().Set("Cache-Control", "private, no-store")
					png.Encode(w, image.NewRGBA(image.Rect(0, 0, 1, 1)))
					return
				}
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(tt.status)
			}))
			defer server.Close()
			p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}}
			img, err := p.getCoverArtWithDelays("artist-1", 300, []time.Duration{0, 0, 0})
			if calls != tt.calls {
				t.Errorf("calls=%d want %d", calls, tt.calls)
			}
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil || img == nil {
				t.Fatalf("image=%v error=%v", img, err)
			}
			if tt.placeholder && !tt.succeeds {
				if policy, ok := img.(interface{ Cacheable() bool }); !ok || policy.Cacheable() {
					t.Fatal("temporary placeholder was cacheable")
				}
			}
			if tt.succeeds && img.Bounds().Dx() != 2 {
				t.Fatal("did not return resolved portrait")
			}
		})
	}
}

func TestAlbumArtworkRequestAndDecodingCompatibility(t *testing.T) {
	for _, passwordAuth := range []bool{false, true} {
		for _, contentType := range []string{"image/jpeg", "application/octet-stream", "text/plain", "application/json"} {
			t.Run(fmt.Sprintf("password=%v/type=%s", passwordAuth, contentType), func(t *testing.T) {
				var queries []string
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/ping") {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"subsonic-response":{"status":"ok","version":"1.16.1"}}`)
						return
					}
					if r.URL.Path != "/bridge/rest/getCoverArt" {
						t.Errorf("path=%s", r.URL.Path)
					}
					q := r.URL.Query()
					if q.Get("u") != "tester" || q.Get("id") != "album-cover" || q.Get("size") != "300" || q.Get("c") != "Supersonic" {
						t.Error("missing cover request parameters")
					}
					if passwordAuth {
						if q.Get("p") != "test-password" {
							t.Error("password authentication missing")
						}
					} else if q.Get("t") == "" || q.Get("s") == "" {
						t.Error("token authentication missing")
					}
					queries = append(queries, r.URL.RawQuery)
					w.Header().Set("Content-Type", contentType)
					w.Header().Set("Cache-Control", "no-store")
					jpeg.Encode(w, image.NewRGBA(image.Rect(0, 0, 3, 3)), nil)
				}))
				defer server.Close()
				client := &api.Client{Client: server.Client(), BaseUrl: server.URL + "/bridge", UseJSON: true, User: "tester", ClientName: "Supersonic", PasswordAuth: passwordAuth}
				if err := client.Authenticate("test-password"); err != nil {
					t.Fatal(err)
				}
				if _, err := client.GetCoverArt("album-cover", map[string]string{"size": "300"}); err != nil {
					t.Fatalf("original client: %v", err)
				}
				p := &subsonicMediaProvider{client: client}
				img, err := p.getCoverArtWithDelays("album-cover", 300, []time.Duration{0, 0, 0})
				if err != nil || img == nil {
					t.Fatalf("new client rejected original-compatible JPEG: %v", err)
				}
				if len(queries) != 2 {
					t.Fatalf("expected one old and one new request; got %d", len(queries))
				}
				if queries[0] != queries[1] {
					t.Fatal("authenticated query differs from original")
				}
				if img.Bounds().Dx() != 3 {
					t.Fatal("wrong decoded image")
				}
			})
		}
	}
}
