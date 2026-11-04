package subsonic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	api "github.com/supersonic-app/go-subsonic/subsonic"
)

const supportedAlbumQuality = `{"subsonic-response":{"status":"ok","openSubsonicExtensions":[{"name":"appleMusicAlbumAudioQuality","versions":[1]}]}}`

func qualityBody(id, source string, available any) string {
	body, _ := json.Marshal(map[string]any{"subsonic-response": map[string]any{"status": "ok", "albumAudioQuality": map[string]any{"id": id, "source": source, "available": available}}})
	return string(body)
}

func TestAlbumAudioQualityValidation(t *testing.T) {
	const id = "apple:album/123?x=&y=日本語"
	valid := qualityBody(id, "apple-music-catalog", []string{"dolby-atmos", "dolby-audio", "hi-res-lossless", "lossless"})
	tests := []struct {
		name, body string
		status     int
		want       []string
		wantErr    bool
	}{
		{"all formats", valid, 200, []string{"dolby-atmos", "dolby-audio", "hi-res-lossless", "lossless"}, false},
		{"empty availability", qualityBody(id, "apple-music-catalog", []string{}), 200, []string{}, false},
		{"duplicate formats", qualityBody(id, "apple-music-catalog", []string{"lossless", "lossless"}), 200, []string{"lossless"}, false},
		{"wrong ID", qualityBody("another", "apple-music-catalog", []string{"lossless"}), 200, nil, true},
		{"wrong source", qualityBody(id, "local-file", []string{"lossless"}), 200, nil, true},
		{"unknown enum", qualityBody(id, "apple-music-catalog", []string{"lossless", "lossy"}), 200, nil, true},
		{"null availability", qualityBody(id, "apple-music-catalog", nil), 200, nil, true},
		{"wrong availability type", qualityBody(id, "apple-music-catalog", "lossless"), 200, nil, true},
		{"missing status", `{"subsonic-response":{}}`, 200, nil, true},
		{"failed", `{"subsonic-response":{"status":"failed","error":{"code":70}}}`, 200, nil, true},
		{"error despite ok", strings.Replace(valid, `"status":"ok"`, `"status":"ok","error":{"code":70}`, 1), 200, nil, true},
		{"missing payload", `{"subsonic-response":{"status":"ok"}}`, 200, nil, true},
		{"missing wrapper", `{"status":"ok"}`, 200, nil, true},
		{"malformed JSON", `{`, 200, nil, true},
		{"empty body", "", 200, nil, true},
		{"XML", `<subsonic-response status="ok"/>`, 200, nil, true},
		{"trailing JSON", valid + `{}`, 200, nil, true},
		{"oversize", valid + strings.Repeat(" ", maxAlbumAudioQualityResponseBytes), 200, nil, true},
		{"HTTP failure", valid, 500, nil, true},
		{"HTTP no content", "", 204, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var discovery, requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method=%s", r.Method)
				}
				q := r.URL.Query()
				if q.Get("f") != "json" || q.Get("u") != "test-user" || q.Get("c") != "quality-test" {
					t.Errorf("missing standard params: %v", q)
				}
				switch r.URL.Path {
				case "/rest/getOpenSubsonicExtensions":
					discovery.Add(1)
					fmt.Fprint(w, supportedAlbumQuality)
				case "/rest/getAlbumAudioQuality":
					requests.Add(1)
					if got := q["id"]; len(got) != 1 || got[0] != id {
						t.Errorf("id=%v", got)
					}
					w.WriteHeader(tt.status)
					fmt.Fprint(w, tt.body)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := &api.Client{Client: server.Client(), BaseUrl: server.URL, User: "test-user", ClientName: "quality-test"}
			p := &subsonicMediaProvider{client: client}
			for range 2 {
				got, err := p.GetAlbumAudioQuality(id)
				if (err != nil) != tt.wantErr {
					t.Fatalf("error=%v, wantErr=%v", err, tt.wantErr)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("got=%v want=%v", got, tt.want)
				}
			}
			if client.UseJSON {
				t.Error("mutated shared client format")
			}
			if discovery.Load() != 1 || requests.Load() != 2 {
				t.Errorf("discovery=%d requests=%d", discovery.Load(), requests.Load())
			}
		})
	}
}

func TestAlbumAudioQualityUnsupported(t *testing.T) {
	for _, body := range []string{
		`{"subsonic-response":{"status":"ok","openSubsonicExtensions":[]}}`,
		strings.Replace(supportedAlbumQuality, `[1]`, `[2]`, 1),
		strings.Replace(supportedAlbumQuality, `appleMusicAlbumAudioQuality`, `other`, 1),
		`{"subsonic-response":{"status":"ok","openSubsonicExtensions":[null]}}`,
		`{"subsonic-response":{"status":"failed","error":{"code":70}}}`,
		`invalid`,
	} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/rest/getOpenSubsonicExtensions" {
					t.Errorf("unsupported endpoint called: %s", r.URL.Path)
				}
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL}}
			var wg sync.WaitGroup
			for range 12 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					got, err := p.GetAlbumAudioQuality("album")
					if err != nil || len(got) != 0 {
						t.Errorf("got=%v err=%v", got, err)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != 1 {
				t.Errorf("discovery calls=%d", calls.Load())
			}
		})
	}
}

func TestAlbumAudioQualityEmptyID(t *testing.T) {
	p := &subsonicMediaProvider{}
	got, err := p.GetAlbumAudioQuality("")
	if err != nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestAlbumAudioQualityAuthenticated(t *testing.T) {
	var token, salt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path == "/rest/ping" {
			token, salt = q.Get("t"), q.Get("s")
			fmt.Fprint(w, `{"subsonic-response":{"status":"ok"}}`)
			return
		}
		if token == "" || salt == "" || q.Get("t") != token || q.Get("s") != salt {
			t.Error("authentication not preserved")
		}
		if r.URL.Path == "/rest/getOpenSubsonicExtensions" {
			fmt.Fprint(w, supportedAlbumQuality)
			return
		}
		fmt.Fprint(w, qualityBody("album", "apple-music-catalog", []string{"lossless"}))
	}))
	defer server.Close()
	client := &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}
	if err := client.Authenticate("test-password"); err != nil {
		t.Fatal(err)
	}
	client.UseJSON = false
	p := &subsonicMediaProvider{client: client}
	if got, err := p.GetAlbumAudioQuality("album"); err != nil || len(got) != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestAlbumAudioQualityConcurrentSupported(t *testing.T) {
	var discovery, requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/getOpenSubsonicExtensions" {
			discovery.Add(1)
			fmt.Fprint(w, strings.Replace(supportedAlbumQuality, `[1]`, `[2,1]`, 1))
			return
		}
		requests.Add(1)
		fmt.Fprint(w, qualityBody(r.URL.Query().Get("id"), "apple-music-catalog", []string{"lossless"}))
	}))
	defer server.Close()
	p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL}}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := p.GetAlbumAudioQuality(fmt.Sprint(i))
			if err != nil || !reflect.DeepEqual(got, []string{"lossless"}) {
				t.Errorf("got=%v err=%v", got, err)
			}
		}()
	}
	wg.Wait()
	if discovery.Load() != 1 || requests.Load() != 12 {
		t.Errorf("discovery=%d requests=%d", discovery.Load(), requests.Load())
	}
}

func TestAlbumAudioQualityTransportFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/getOpenSubsonicExtensions" {
			fmt.Fprint(w, supportedAlbumQuality)
			return
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL}}
	if got, err := p.GetAlbumAudioQuality("album"); err == nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}
