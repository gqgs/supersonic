package util

import (
	"context"
	"errors"
	"image"
	"log"
	"time"

	"fyne.io/fyne/v2"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

// ThumbnailLoader is a utility type that exposes a single API to load
// a cover thumbnail by ID. If the image is immediately available in
// the cache, OnLoaded will be called immediately. If it is not,
// OnBeforeLoad will be called first, then OnLoaded will be called async
// once the image is available.
// Any subsequent calls to Load will cancel the previous load if not yet completed.
type ThumbnailLoader struct {
	prevLoadCancel  context.CancelFunc
	im              ImageFetcher
	request         uint64
	cacheGeneration uint64
	invalidated     bool
	retryTimer      *time.Timer

	// CanRetry optionally limits deferred requests to cards still in the viewport.
	CanRetry     func() bool
	OnBeforeLoad func()
	OnLoaded     func(image.Image)
}

// Image backend interface for the ThumbnailLoader
// impl: backend.ImageManager
type ImageFetcher interface {
	GetCoverThumbnailFromCache(string) (image.Image, bool)
	GetCoverThumbnailAsync(string, func(image.Image, error)) context.CancelFunc
}

func NewThumbnailLoader(im ImageFetcher, onLoaded func(image.Image)) ThumbnailLoader {
	return ThumbnailLoader{im: im, OnLoaded: onLoaded}
}

// NeedsReload reports explicit cache invalidation, not ordinary paint refreshes.
func (i *ThumbnailLoader) NeedsReload() bool {
	return i.invalidated || i.cacheGeneration != i.generation()
}

func (i *ThumbnailLoader) generation() uint64 {
	if source, ok := i.im.(interface{ ImageCacheGeneration() uint64 }); ok {
		return source.ImageCacheGeneration()
	}
	return 0
}

// Cancel stops outstanding work and invalidates callbacks already queued on the UI thread.
func (i *ThumbnailLoader) Cancel() {
	if i.retryTimer != nil {
		i.retryTimer.Stop()
		i.retryTimer = nil
	}
	i.invalidated = true
	i.request++
	if i.prevLoadCancel != nil {
		i.prevLoadCancel()
		i.prevLoadCancel = nil
	}
}

func (i *ThumbnailLoader) Load(coverID string) {
	i.load(coverID, 3)
}

func (i *ThumbnailLoader) load(coverID string, retries int) {
	i.Cancel()
	i.invalidated = false
	request := i.request
	i.cacheGeneration = i.generation()
	if coverID == "" {
		i.callOnLoaded(nil)
		return
	}
	if img, ok := i.im.GetCoverThumbnailFromCache(coverID); ok {
		i.callOnLoaded(img)
		return
	}
	if i.OnBeforeLoad != nil {
		i.OnBeforeLoad()
	}
	i.prevLoadCancel = i.im.GetCoverThumbnailAsync(coverID, func(img image.Image, err error) {
		fyne.Do(func() {
			if request != i.request {
				return
			}
			if err != nil {
				if !mediaprovider.IsExpectedArtworkError(err) {
					log.Printf("Error loading cover image: %s", err.Error())
				}
				i.callOnLoaded(nil)
				var cooldown *mediaprovider.ArtworkRetryError
				// Retry only pending artwork, never known missing art or generic failures.
				// Three deferred attempts bound work for a stationary card.
				if request == i.request && retries > 0 && errors.Is(err, mediaprovider.ErrArtworkPending) && errors.As(err, &cooldown) {
					delay := time.Until(cooldown.RetryAt)
					if delay < time.Second {
						delay = time.Second
					}
					if delay <= 5*time.Minute {
						i.retryTimer = time.AfterFunc(delay, func() {
							fyne.Do(func() {
								if request == i.request {
									if i.CanRetry != nil && !i.CanRetry() {
										i.invalidated = true
										return
									}
									i.load(coverID, retries-1)
								}
							})
						})
					}
				}
			} else {
				i.callOnLoaded(img)
			}
			if request == i.request && i.prevLoadCancel != nil {
				i.prevLoadCancel() // Cancel the context to release resources if not already cancelled
			}
		})
	})
}

func (i *ThumbnailLoader) callOnLoaded(im image.Image) {
	if i.OnLoaded != nil {
		i.OnLoaded(im)
	}
}
