package subsonic

import (
	"encoding/json"
	"fmt"
	api "github.com/supersonic-app/go-subsonic/subsonic"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestArtistIndexDoesNotPrefetchInvisibleArtwork(t *testing.T) {
	const total = 1000
	artists := make([]map[string]string, total)
	for i := range artists {
		artists[i] = map[string]string{"id": fmt.Sprint(i), "name": fmt.Sprint(i), "coverArt": fmt.Sprintf("artist-image:am:br:lossless:artist:%d", i)}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{"status": "ok", "version": "1.16.1", "artists": map[string]any{"index": []any{map[string]any{"name": "A", "artist": artists}}}}})
	}))
	defer server.Close()
	prefetched := make(chan string, total)
	p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true}, prefetchCoverCB: func(id string) { prefetched <- id }}
	iter := p.IterateArtists(mediaprovider.ArtistSortNameAZ, mediaprovider.NewArtistFilter(mediaprovider.ArtistFilterOptions{}))
	if iter.Next() == nil {
		t.Fatal("no first artist")
	}
	select {
	case id := <-prefetched:
		t.Fatalf("unexpected eager artwork request: %s", id)
	case <-time.After(30 * time.Millisecond):
	}
	t.Logf("UI consumed 1 of %d artists; eager artwork requests=0", total)
}
