package backend

import (
	"context"
	"fmt"
	"image"
	"math"
)

// CoverArtSize rounds physical display pixels up to a small, bounded set of
// request sizes. Stable buckets avoid a new download for every resize pixel.
func CoverArtSize(logicalPixels, scale float32) int {
	if scale <= 0 || math.IsNaN(float64(scale)) {
		scale = 1
	}
	pixels := float64(logicalPixels) * float64(scale)
	for _, size := range [...]int{128, 256, 300, 400, 512, 600, 1024, 1200, 1400} {
		if pixels <= float64(size) {
			return size
		}
	}
	return 1400
}

func coverCacheKey(id string, size int) string {
	if size == coverArtThumbnailSize {
		return id
	}
	// A NUL cannot occur in a server cover ID used as a filesystem name.
	return fmt.Sprintf("\x00%d:%s", size, id)
}

// GetCoverArtSizedFromCache never substitutes a smaller, previously cached image.
func (i *ImageManager) GetCoverArtSizedFromCache(id string, size int) (image.Image, bool) {
	size = CoverArtSize(float32(size), 1)
	img, err := i.thumbnailCache.GetExtendTTL(coverCacheKey(id, size), i.thumbnailCache.DefaultTTL)
	return img, err == nil && img != nil
}

func (i *ImageManager) GetCoverArtSized(id string, size int) (image.Image, error) {
	size = CoverArtSize(float32(size), 1)
	if img, ok := i.GetCoverArtSizedFromCache(id, size); ok {
		return img, nil
	}
	return i.fetchAndCacheCoverFromDiskOrServer(context.Background(), id, i.thumbnailCache.DefaultTTL, nil, size)
}

// GetCoverArtSizedAsync follows the thumbnail cancellation contract.
func (i *ImageManager) GetCoverArtSizedAsync(id string, size int, cb func(image.Image, error)) context.CancelFunc {
	size = CoverArtSize(float32(size), 1)
	ctx, cancel := context.WithCancel(context.Background())
	generation := i.ImageCacheGeneration()
	deliver := func(img image.Image, err error) {
		if ctx.Err() == nil && generation == i.ImageCacheGeneration() {
			cb(img, err)
		}
	}
	go func() {
		if img, ok := i.GetCoverArtSizedFromCache(id, size); ok {
			if ctx.Err() == nil {
				deliver(img, nil)
			}
		} else {
			i.fetchAndCacheCoverFromDiskOrServer(ctx, id, i.thumbnailCache.DefaultTTL, deliver, size)
		}
	}()
	return cancel
}
