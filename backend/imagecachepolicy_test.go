package backend

import (
	"context"
	"github.com/google/uuid"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"image"
	"os"
	"testing"
	"time"
)

type temporaryImage struct{ image.Image }

func (temporaryImage) Cacheable() bool { return false }
func TestImageCachePolicy(t *testing.T) {
	real := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if !imageIsCacheable(real) {
		t.Fatal("real image not cacheable")
	}
	if imageIsCacheable(temporaryImage{real}) || imageIsCacheable(nil) {
		t.Fatal("temporary or nil image cacheable")
	}
}

type portraitProvider struct {
	mediaprovider.MediaProvider
	img image.Image
}

func (p portraitProvider) GetCoverArt(string, int) (image.Image, error) { return p.img, nil }
func TestTemporaryPortraitIsDeliveredButNotCached(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img := temporaryImage{image.NewRGBA(image.Rect(0, 0, 2, 2))}
	manager := NewImageManager(ctx, &ServerManager{Server: portraitProvider{img: img}, ServerID: uuid.New()}, t.TempDir())
	delivered := false
	if _, err := manager.fetchAndCacheCoverFromServer(ctx, "portrait", time.Hour, func(got image.Image, err error) { delivered = got != nil && err == nil }); err != nil {
		t.Fatal(err)
	}
	if !delivered {
		t.Fatal("image not delivered")
	}
	if _, ok := manager.thumbnailCache.cache["portrait"]; ok {
		t.Fatal("temporary image cached in memory")
	}
	if _, err := os.Stat(manager.filePathForCover("portrait")); !os.IsNotExist(err) {
		t.Fatalf("temporary image cached on disk: %v", err)
	}
	if _, err := manager.getFullSizeCoverArtFromServer(ctx, "portrait", nil); err != nil {
		t.Fatal(err)
	}
	if manager.cachedFullSizeCover != nil {
		t.Fatal("temporary full-size image cached")
	}
}
