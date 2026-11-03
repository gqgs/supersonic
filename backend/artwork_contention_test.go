package backend

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	api "github.com/supersonic-app/go-subsonic/subsonic"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	provider "github.com/supersonic-app/supersonic/backend/mediaprovider/subsonic"
)

type originalCoverProvider struct {
	mediaprovider.MediaProvider
	client *api.Client
}

func (p originalCoverProvider) GetCoverArt(id string, size int) (image.Image, error) {
	return p.client.GetCoverArt(id, nil)
}
func TestCancelledPortraitBurstReleasesAlbumCapacity(t *testing.T) {
	for _, original := range []bool{true, false} {
		name := "current"
		if original {
			name = "pre-retry"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{}, 5)
			release := make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("id") == "album" {
					w.Header().Set("Content-Type", "image/png")
					png.Encode(w, image.NewRGBA(image.Rect(0, 0, 400, 400)))
					return
				}
				n := calls.Add(1)
				if n <= 5 {
					entered <- struct{}{}
					<-release
				}
				w.WriteHeader(503)
			}))
			defer server.Close()
			client := &api.Client{Client: server.Client(), BaseUrl: server.URL, UseJSON: true, User: "bridge", ClientName: "Supersonic"}
			mp := provider.SubsonicMediaProvider(client)
			if original {
				mp = originalCoverProvider{MediaProvider: mp, client: client}
			}
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			manager := NewImageManager(ctx, &ServerManager{Server: mp}, t.TempDir())
			cancels := make([]context.CancelFunc, 5)
			for i := range cancels {
				cancels[i] = manager.GetCoverThumbnailAsync(fmt.Sprintf("portrait-%d", i), func(image.Image, error) {})
			}
			for range cancels {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("requests not started")
				}
			}
			for _, cancel := range cancels {
				cancel()
			}
			start := time.Now()
			done := make(chan error, 1)
			cancel := manager.GetCoverThumbnailAsync("album", func(img image.Image, err error) { done <- err })
			defer cancel()
			close(release)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("album starved")
			}
			t.Logf("already-cancelled portrait HTTP attempts=%d; ready album waited=%s", calls.Load(), time.Since(start))
			if !original && time.Since(start) > time.Second {
				t.Fatal("cancelled portraits delayed album")
			}
		})
	}
}

func TestConcurrentCoverViewersShareWorkAndCancelIndependently(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "image/png")
		png.Encode(w, image.NewRGBA(image.Rect(0, 0, 20, 20)))
	}))
	defer server.Close()
	cli := &api.Client{Client: server.Client(), BaseUrl: server.URL}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	manager := NewImageManager(ctx, &ServerManager{Server: provider.SubsonicMediaProvider(cli)}, t.TempDir())
	const viewers = 20
	done := make(chan error, viewers)
	cancels := make([]context.CancelFunc, viewers)
	for i := range cancels {
		cancels[i] = manager.GetCoverThumbnailAsync("same-cover", func(_ image.Image, err error) { done <- err })
	}
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	<-entered
	deadline := time.Now().Add(time.Second)
	for {
		manager.coverFlightsMu.Lock()
		waiters := 0
		for _, f := range manager.coverFlights {
			waiters += f.waiters
		}
		manager.coverFlightsMu.Unlock()
		if waiters == viewers {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("viewers did not join shared request")
		}
		time.Sleep(time.Millisecond)
	}
	cancels[0]()
	close(release)
	for n := 1; n < viewers; n++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("one viewer cancelled others")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("%d viewers made %d HTTP calls", viewers, calls.Load())
	}
	t.Logf("%d simultaneous viewers: %d HTTP request, one cancelled independently", viewers, calls.Load())
}

func TestPendingArtistCardsReserveAlbumCapacity(t *testing.T) {
	entered := make(chan struct{}, 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Query().Get("id"), "artist-image:") {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "image/png")
		png.Encode(w, image.NewRGBA(image.Rect(0, 0, 400, 400)))
	}))
	defer server.Close()
	cli := &api.Client{Client: server.Client(), BaseUrl: server.URL}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	manager := NewImageManager(ctx, &ServerManager{Server: provider.SubsonicMediaProvider(cli)}, t.TempDir())
	var cancels []context.CancelFunc
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	for n := 0; n < 20; n++ {
		cancels = append(cancels, manager.GetCoverThumbnailAsync(fmt.Sprintf("artist-image:%d", n), func(image.Image, error) {}))
	}
	for n := 0; n < 2; n++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("artist requests not started")
		}
	}
	start := time.Now()
	done := make(chan error, 1)
	cancels = append(cancels, manager.GetCoverThumbnailAsync("album", func(_ image.Image, err error) { done <- err }))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("artist grid blocked album capacity")
	}
	select {
	case <-entered:
		t.Fatal("more than two artist requests consumed reserved capacity")
	default:
	}
	t.Logf("20 pending artist cards: ready album returned in %s without cancelling artist viewers", time.Since(start))
}
