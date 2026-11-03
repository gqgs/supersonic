package subsonic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	api "github.com/supersonic-app/go-subsonic/subsonic"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

func TestArtistSearchRequestsOnlyArtistsAndLoadsReleasesOnOpen(t *testing.T) {
	var mu sync.Mutex
	var offsets []int
	details := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		response := map[string]any{"status": "ok", "version": "1.16.1"}
		switch r.URL.Path {
		case "/rest/search3":
			q := r.URL.Query()
			for key, want := range map[string]string{"artistCount": "50", "albumCount": "0", "songCount": "0", "query": "artist", "musicFolderId": "library"} {
				if got := q.Get(key); got != want {
					t.Errorf("%s=%q, want %q", key, got, want)
				}
			}
			offset, _ := strconv.Atoi(q.Get("artistOffset"))
			offsets = append(offsets, offset)
			var artists []map[string]any
			start, n := offset, 50
			// Include a duplicate-only page: later matches must still be reached.
			if offset == 50 {
				start = 0
			}
			if offset == 100 {
				n = 5
			}
			for i := 0; i < n; i++ {
				artists = append(artists, map[string]any{"id": fmt.Sprint(start + i), "name": fmt.Sprintf("Artist %d", start+i), "albumCount": 10})
			}
			response["searchResult3"] = map[string]any{"artist": artists}
		case "/rest/getArtist":
			details++
			response["artist"] = map[string]any{"id": "0", "name": "Artist 0", "albumCount": 1, "album": []map[string]any{{"id": "release", "name": "Release"}}}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"subsonic-response": response})
	}))
	defer server.Close()
	provider := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}, currentLibraryID: "library"}
	iter := provider.SearchArtists("artist", mediaprovider.NewArtistFilter(mediaprovider.ArtistFilterOptions{}))
	started := time.Now()
	first := iter.Next()
	t.Logf("first artist latency against local HTTP server: %s", time.Since(started))
	if first == nil || first.ID != "0" {
		t.Fatal("missing first artist")
	}
	mu.Lock()
	if len(offsets) != 1 || details != 0 {
		t.Errorf("first result: offsets=%v details=%d", offsets, details)
	}
	mu.Unlock()
	ids := []string{first.ID}
	// The grid initially asks for 36 cards; one 50-artist page covers it.
	for len(ids) < 36 {
		artist := iter.Next()
		if artist == nil {
			t.Fatal("initial grid was truncated")
		}
		ids = append(ids, artist.ID)
	}
	mu.Lock()
	if len(offsets) != 1 || details != 0 {
		t.Errorf("initial 36 cards: offsets=%v details=%d", offsets, details)
	}
	mu.Unlock()
	for artist := iter.Next(); artist != nil; artist = iter.Next() {
		ids = append(ids, artist.ID)
	}
	if len(ids) != 55 {
		t.Fatalf("got %d artists, want 55 unique matches", len(ids))
	}
	if iter.Next() != nil {
		t.Fatal("exhausted iterator returned another artist")
	}
	mu.Lock()
	if !reflect.DeepEqual(offsets, []int{0, 50, 100}) || details != 0 {
		t.Errorf("search requests: offsets=%v details=%d", offsets, details)
	}
	mu.Unlock()
	artist, err := provider.GetArtist(first.ID)
	if err != nil || len(artist.Albums) != 1 {
		t.Fatalf("opening artist: %v, %v", artist, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if details != 1 {
		t.Fatalf("got %d detail calls, want 1 on open", details)
	}
}

func TestArtistSearchEmptyArtistsIgnoresUnrequestedResults(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","searchResult3":{"album":[{"id":"unused"}],"song":[{"id":"unused"}]}}}`)
	}))
	defer server.Close()
	provider := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}}
	iter := provider.SearchArtists("absent", mediaprovider.NewArtistFilter(mediaprovider.ArtistFilterOptions{}))
	for range 3 {
		if iter.Next() != nil {
			t.Fatal("non-artist result returned")
		}
	}
	if requests != 1 {
		t.Fatalf("empty search repeated %d requests", requests)
	}
}

func TestMixedSearchRetainsServerDefaultCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, key := range []string{"artistCount", "albumCount", "songCount"} {
			if r.URL.Query().Has(key) {
				t.Errorf("mixed search changed %s", key)
			}
		}
		fmt.Fprint(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","searchResult3":{"album":[{"id":"album"}]}}}`)
	}))
	defer server.Close()
	iter := searchIterBase{query: "mixed", s: &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}}
	if result := iter.fetchResults(); result == nil || len(result.Album) != 1 {
		t.Fatal("mixed search lost album results")
	}
}

func TestArtistSearchPaginationBoundaries(t *testing.T) {
	for _, total := range []int{0, 5, 50, 105} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			var mu sync.Mutex
			var offsets []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				offset, _ := strconv.Atoi(r.URL.Query().Get("artistOffset"))
				mu.Lock()
				offsets = append(offsets, offset)
				mu.Unlock()
				var artists []map[string]string
				for i := offset; i < min(offset+50, total); i++ {
					artists = append(artists, map[string]string{"id": strconv.Itoa(i), "name": "Artist"})
				}
				json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{"status": "ok", "version": "1.16.1", "searchResult3": map[string]any{"artist": artists}}})
			}))
			defer server.Close()
			provider := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}}
			iter := provider.SearchArtists("artist", mediaprovider.NewArtistFilter(mediaprovider.ArtistFilterOptions{}))
			for i := 0; i < total; i++ {
				artist := iter.Next()
				if artist == nil || artist.ID != strconv.Itoa(i) {
					t.Fatalf("artist %d: %v", i, artist)
				}
			}
			if iter.Next() != nil || iter.Next() != nil {
				t.Fatal("iterator not exhausted")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(offsets) != total/50+1 {
				t.Fatalf("offsets=%v, expected %d requests", offsets, total/50+1)
			}
			for i, offset := range offsets {
				if offset != i*50 {
					t.Fatalf("offsets=%v", offsets)
				}
			}
		})
	}
}
