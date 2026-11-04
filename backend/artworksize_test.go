package backend

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

func TestCoverArtSizePhysicalPixels(t *testing.T) {
	for _, tc := range []struct {
		logical, scale float32
		want           int
	}{
		{40, 1, 128}, {40, 2, 128}, {150, 1, 256}, {150, 2, 300},
		{200, 2, 400}, {256, 2, 512}, {300, 2, 600},
		{565, 2, 1200}, {700, 2, 1400}, {2000, 2, 1400},
		{301, 1, 400}, {399, 1, 400}, {200, 0, 256},
	} {
		if got := CoverArtSize(tc.logical, tc.scale); got != tc.want {
			t.Errorf("%v x %v = %d, want %d", tc.logical, tc.scale, got, tc.want)
		}
	}
}

type sizedArtProvider struct {
	mediaprovider.MediaProvider
	mu      sync.Mutex
	sizes   []int
	started chan int
	release chan struct{}
}

func (p *sizedArtProvider) GetCoverArt(_ string, size int) (image.Image, error) {
	p.mu.Lock()
	p.sizes = append(p.sizes, size)
	p.mu.Unlock()
	if p.started != nil {
		p.started <- size
		<-p.release
	}
	return image.NewRGBA(image.Rect(0, 0, size, size)), nil
}
func newSizedManager(t *testing.T, p *sizedArtProvider) *ImageManager {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewImageManager(ctx, &ServerManager{Server: p, ServerID: uuid.New()}, t.TempDir())
}
func TestSizedArtworkSeparatesMemoryAndDisk(t *testing.T) {
	p := &sizedArtProvider{}
	m := newSizedManager(t, p)
	for _, size := range []int{128, 300, 600, 1200} {
		img, err := m.GetCoverArtSized("same-cover", size)
		if err != nil || img.Bounds().Dx() != size {
			t.Fatalf("size %d: %v %v", size, img, err)
		}
	}
	m.ClearInMemoryCache()
	for _, size := range []int{1200, 600, 300, 128} {
		img, err := m.GetCoverArtSized("same-cover", size)
		if err != nil || img.Bounds().Dx() != size {
			t.Fatalf("disk size %d: %v %v", size, img, err)
		}
	}
	if len(p.sizes) != 4 {
		t.Fatalf("disk cache missed: %v", p.sizes)
	}
	if _, ok := m.GetCoverArtSizedFromCache("same-cover", 400); ok {
		t.Fatal("wrong size cache hit")
	}
	if img, ok := m.GetCoverThumbnailFromCache("same-cover"); !ok || img.Bounds().Dx() != 300 {
		t.Fatal("legacy thumbnail changed")
	}
}
func TestSizedArtworkFlightsAndInvalidation(t *testing.T) {
	p := &sizedArtProvider{started: make(chan int, 2), release: make(chan struct{})}
	m := newSizedManager(t, p)
	done := make(chan struct{}, 2)
	for _, size := range []int{128, 600} {
		go func(size int) { m.GetCoverArtSized("same", size); done <- struct{}{} }(size)
	}
	for n := 0; n < 2; n++ {
		select {
		case <-p.started:
		case <-time.After(time.Second):
			t.Fatal("different sizes shared an in-flight request")
		}
	}
	m.ClearInMemoryCache()
	close(p.release)
	for n := 0; n < 2; n++ {
		<-done
	}
	// Clear cancels the shared contexts even for a provider that ignores cancellation.
	m.coverFlightsMu.Lock()
	m.coverFlightsMu.Unlock()
	for _, size := range []int{128, 600} {
		if _, ok := m.GetCoverArtSizedFromCache("same", size); ok {
			t.Fatal("stale response repopulated cache")
		}
	}
}
func TestSizedArtworkCancelledCallback(t *testing.T) {
	p := &sizedArtProvider{started: make(chan int, 1), release: make(chan struct{})}
	m := newSizedManager(t, p)
	called := make(chan struct{}, 1)
	cancel := m.GetCoverArtSizedAsync("cancel", 600, func(image.Image, error) { called <- struct{}{} })
	<-p.started
	cancel()
	close(p.release)
	select {
	case <-called:
		t.Fatal("cancelled request delivered callback")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestSizedArtworkMemoryBoundOverridesEntryFloor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := ImageCache{MinSize: 24, MaxSize: 150, MaxBytes: 16 * 1024 * 1024}
	cache.Init(ctx, time.Hour)
	for _, id := range []string{"one", "two", "three"} {
		cache.Set(id, image.NewRGBA(image.Rect(0, 0, 1400, 1400)))
	}
	if len(cache.cache) != 1 {
		t.Fatalf("large images exceeded decoded memory budget: %d retained", len(cache.cache))
	}
	cache.Set("tiny", image.NewRGBA(image.Rect(0, 0, 128, 128)))
	if !cache.Has("tiny") {
		t.Fatal("small artwork not retained")
	}
}

func TestLargeArtworkDiskCachePreservesHigherQuality(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			source.SetGray(x, y, color.Gray{Y: uint8((x*31 + y*53) % 256)})
		}
	}
	m := NewImageManager(ctx, &ServerManager{Server: portraitProvider{img: source}, ServerID: uuid.New()}, t.TempDir())
	for _, size := range []int{128, 300, 600, 1400} {
		if _, err := m.GetCoverArtSized("quality", size); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(m.filePathForCover("quality", size))
		if err != nil {
			t.Fatal(err)
		}
		var want bytes.Buffer
		var opts *jpeg.Options
		if size > 300 {
			opts = &jpeg.Options{Quality: 95}
		}
		if err := jpeg.Encode(&want, source, opts); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, want.Bytes()) {
			t.Fatalf("size %d used incorrect JPEG cache quality", size)
		}
	}
	if m.maxOnDiskCacheSizeBytes != 50*1_048_576 {
		t.Fatal("disk-cache budget changed")
	}
}
