package util

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

type thumbnailClock struct {
	now    time.Time
	timers []*thumbnailTimer
}
type thumbnailTimer struct {
	at       time.Time
	callback func()
	stopped  bool
}

func (t *thumbnailTimer) Stop() bool { old := t.stopped; t.stopped = true; return !old }
func (c *thumbnailClock) after(d time.Duration, f func()) interface{ Stop() bool } {
	t := &thumbnailTimer{at: c.now.Add(d), callback: f}
	c.timers = append(c.timers, t)
	return t
}
func (c *thumbnailClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if !t.stopped && !t.at.After(c.now) {
			t.stopped = true
			t.callback()
		}
	}
}

type thumbnailFetcher struct {
	calls      int
	generation uint64
	callback   func(image.Image, error)
}

func (f *thumbnailFetcher) ImageCacheGeneration() uint64                          { return f.generation }
func (f *thumbnailFetcher) GetCoverThumbnailFromCache(string) (image.Image, bool) { return nil, false }
func (f *thumbnailFetcher) GetCoverThumbnailAsync(_ string, cb func(image.Image, error)) context.CancelFunc {
	f.calls++
	f.callback = cb
	return func() {}
}

func TestThumbnailLongCooldownRecoveryAndCancellation(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	for _, delay := range []time.Duration{60 * time.Second, 300 * time.Second, 3600 * time.Second} {
		for _, action := range []string{"recover", "cancel", "offscreen", "recycle", "generation"} {
			t.Run(delay.String()+"/"+action, func(t *testing.T) {
				clock := &thumbnailClock{now: time.Unix(1000, 0)}
				fetch := &thumbnailFetcher{}
				loaded := false
				loader := NewThumbnailLoader(fetch, func(img image.Image) { loaded = img != nil })
				loader.now = func() time.Time { return clock.now }
				loader.afterFunc = clock.after
				visible := true
				loader.CanRetry = func() bool { return visible }
				loader.Load("artist-release:1")
				fetch.callback(nil, &mediaprovider.ArtworkRetryError{Err: mediaprovider.ErrArtworkPending, RetryAt: clock.now.Add(delay)})
				if len(clock.timers) != 1 {
					t.Fatal("no deferred recovery")
				}
				clock.advance(delay - time.Second)
				if fetch.calls != 1 {
					t.Fatal("retried before server deadline")
				}
				switch action {
				case "cancel":
					loader.Cancel()
				case "offscreen":
					visible = false
				case "recycle":
					loader.Load("artist-release:2")
				case "generation":
					fetch.generation++
				}
				before := fetch.calls
				clock.advance(time.Second)
				if action == "recover" {
					if fetch.calls != before+1 {
						t.Fatal("visible card stranded")
					}
					fetch.callback(image.NewRGBA(image.Rect(0, 0, 1, 1)), nil)
					if !loaded {
						t.Fatal("recovered image not displayed")
					}
				} else if fetch.calls != before {
					t.Fatal("obsolete or hidden card retried")
				}
				if action == "offscreen" && !loader.NeedsReload() {
					t.Fatal("offscreen card cannot reload on return")
				}
				loader.Cancel()
			})
		}
	}
}

func TestThumbnailRetryBoundAndErrorDistinctions(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	for _, err := range []error{mediaprovider.ErrArtworkPending, mediaprovider.ErrArtworkUnavailable, errors.New("authentication or unexpected failure")} {
		clock := &thumbnailClock{now: time.Unix(1000, 0)}
		fetch := &thumbnailFetcher{}
		loader := NewThumbnailLoader(fetch, nil)
		loader.now = func() time.Time { return clock.now }
		loader.afterFunc = clock.after
		loader.Load("artist-release:1")
		for n := 0; n < 5; n++ {
			fetch.callback(nil, &mediaprovider.ArtworkRetryError{Err: err, RetryAt: clock.now.Add(300 * time.Second)})
			clock.advance(300 * time.Second)
		}
		want := 1
		if errors.Is(err, mediaprovider.ErrArtworkPending) {
			want = 4
		}
		if fetch.calls != want {
			t.Fatalf("%v: calls %d want %d", err, fetch.calls, want)
		}
		loader.Cancel()
	}
}
