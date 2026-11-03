package subsonic

import (
	"encoding/json"
	"fmt"
	api "github.com/supersonic-app/go-subsonic/subsonic"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestAlbumSearchAvoidsExpansionAndPaginates(t *testing.T) {
	for _, total := range []int{0, 5, 50, 105} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			var mu sync.Mutex
			var offsets []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/rest/search3" {
					t.Errorf("unexpected detail request: %s", r.URL.Path)
					http.Error(w, "unexpected", 500)
					return
				}
				q := r.URL.Query()
				for key, want := range map[string]string{"artistCount": "0", "albumCount": "50", "songCount": "0", "musicFolderId": "library", "query": "album"} {
					if q.Get(key) != want {
						t.Errorf("%s=%q, want %q", key, q.Get(key), want)
					}
				}
				offset, _ := strconv.Atoi(q.Get("albumOffset"))
				mu.Lock()
				offsets = append(offsets, offset)
				mu.Unlock()
				var albums []map[string]any
				for i := offset; i < min(offset+50, total); i++ {
					albums = append(albums, map[string]any{"id": strconv.Itoa(i), "name": "Album", "year": 2020})
				}
				// A server ignoring zero counts must still not trigger client expansion.
				json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{"status": "ok", "version": "1.16.1", "searchResult3": map[string]any{"album": albums, "artist": []map[string]string{{"id": "unused"}}, "song": []map[string]string{{"id": "unused", "albumId": "unused"}}}}})
			}))
			defer server.Close()
			p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}, currentLibraryID: "library"}
			iter := p.SearchAlbums("album", mediaprovider.NewAlbumFilter(mediaprovider.AlbumFilterOptions{}))
			started := time.Now()
			for i := 0; i < total; i++ {
				album := iter.Next()
				if album == nil || album.ID != strconv.Itoa(i) {
					t.Fatalf("album %d: %v", i, album)
				}
				if i == 0 {
					t.Logf("first album latency against local HTTP server: %s", time.Since(started))
				}
				if i == 35 {
					mu.Lock()
					if len(offsets) != 1 {
						t.Errorf("36 initial cards used %d requests", len(offsets))
					}
					mu.Unlock()
				}
			}
			if iter.Next() != nil || iter.Next() != nil {
				t.Fatal("iterator not exhausted")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(offsets) != total/50+1 {
				t.Fatalf("offsets=%v", offsets)
			}
			for i, offset := range offsets {
				if offset != i*50 {
					t.Fatalf("offsets=%v", offsets)
				}
			}
		})
	}
}

func TestAlbumSearchSkipsFilteredAndDuplicatePages(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		offset, _ := strconv.Atoi(r.URL.Query().Get("albumOffset"))
		if offset > 150 {
			t.Error("pagination did not terminate")
			http.Error(w, "bad offset", 500)
			return
		}
		var albums []map[string]any
		if offset == 0 {
			for i := 0; i < 50; i++ {
				albums = append(albums, map[string]any{"id": fmt.Sprint(i), "year": 1990})
			}
		}
		if offset == 50 || offset == 100 {
			for i := 0; i < 50; i++ {
				albums = append(albums, map[string]any{"id": fmt.Sprint(50 + i), "year": 2020})
			}
		}
		if offset == 150 {
			albums = append(albums, map[string]any{"id": "last", "year": 2020})
		}
		json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{"status": "ok", "version": "1.16.1", "searchResult3": map[string]any{"album": albums}}})
	}))
	defer server.Close()
	p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}}
	iter := p.SearchAlbums("album", mediaprovider.NewAlbumFilter(mediaprovider.AlbumFilterOptions{MinYear: 2000}))
	ids := map[string]bool{}
	for a := iter.Next(); a != nil; a = iter.Next() {
		if ids[a.ID] {
			t.Fatal("duplicate album")
		}
		ids[a.ID] = true
	}
	if len(ids) != 51 || !ids["last"] || requests != 4 {
		t.Fatalf("unique=%d last=%v requests=%d", len(ids), ids["last"], requests)
	}
}
