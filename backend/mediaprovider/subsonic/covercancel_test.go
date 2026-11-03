package subsonic

import (
	"context"
	api "github.com/supersonic-app/go-subsonic/subsonic"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCoverCancellationStopsHTTPAndRetryWait(t *testing.T) {
	for _, inHTTP := range []bool{true, false} {
		t.Run(map[bool]string{true: "HTTP", false: "retry"}[inHTTP], func(t *testing.T) {
			entered := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				if inHTTP {
					<-r.Context().Done()
				} else {
					w.WriteHeader(503)
				}
			}))
			defer server.Close()
			p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := p.getCoverArtContext(ctx, "cover", 300, []time.Duration{time.Hour}); done <- err }()
			<-entered
			if !inHTTP {
				time.Sleep(20 * time.Millisecond)
			}
			start := time.Now()
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled fetch succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not stop work")
			}
			t.Logf("cancelled %s in %s", map[bool]string{true: "HTTP", false: "retry wait"}[inHTTP], time.Since(start))
		})
	}
}

func TestLongRetryAfterDoesNotOccupyAnArtworkSlot(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(503)
	}))
	defer server.Close()
	p := &subsonicMediaProvider{client: &api.Client{Client: server.Client(), BaseUrl: server.URL}}
	start := time.Now()
	_, err := p.GetCoverArtContext(context.Background(), "artist-image:1", 300)
	if err == nil || calls != 1 || time.Since(start) > time.Second {
		t.Fatalf("long cooldown occupied slot: calls=%d err=%v duration=%s", calls, err, time.Since(start))
	}
}
