package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"github.com/20after4/configdir"
	"github.com/google/uuid"
)

const (
	coverArtThumbnailSize = 300
	cachedImageValidTime  = 24 * time.Hour
	fullSizeCoverExpires  = 5 * time.Minute

	maxConcurrentServerFetches = 5
	defaultDiskCacheSizeBytes  = 50 * 1_048_576
)

// The ImageManager is responsible for retrieving and serving images to the UI layer.
// It maintains an in-memory cache of recently used images for immediate future access,
// and a larger on-disc cache of images that is periodically re-requested from the server.
type ImageManager struct {
	s               *ServerManager
	baseCacheDir    string
	thumbnailCache  ImageCache
	cacheGeneration atomic.Uint64
	coverFlightsMu  sync.Mutex
	coverFlights    map[coverFlightKey]*coverFlight

	cachedFullSizeCover           image.Image
	cachedFullSizeCoverID         string
	cachedFullSizeCoverAccessedAt int64 // unixMillis

	maxOnDiskCacheSizeBytes    int64
	filesWrittenSinceLastPrune bool

	serverFetchSema chan any
	artistFetchSema chan any
}

// NewImageManager returns a new ImageManager.
func NewImageManager(ctx context.Context, s *ServerManager, baseCacheDir string) *ImageManager {
	if err := configdir.MakePath(baseCacheDir); err != nil {
		log.Println("failed to create album cover cache dir")
		baseCacheDir = ""
	}
	i := &ImageManager{
		s:            s,
		baseCacheDir: baseCacheDir,
		thumbnailCache: ImageCache{
			MinSize:    24,
			MaxSize:    150,
			MaxBytes:   64 * 1024 * 1024,
			DefaultTTL: 1 * time.Minute,
		},
		maxOnDiskCacheSizeBytes: defaultDiskCacheSizeBytes,
		serverFetchSema:         make(chan any, maxConcurrentServerFetches),
		artistFetchSema:         make(chan any, 2),
	}
	s.OnLogout(i.ClearInMemoryCache)
	i.thumbnailCache.OnEvictTaskRan = func() {
		i.clearFullSizeCoverIfExpired()
		i.pruneOnDiskCache()
	}
	i.thumbnailCache.Init(ctx, 2*time.Minute)
	return i
}

// SetMaxOnDiskCacheSizeBytes sets the maximum size of the on-disc cover thumbnail cache.
// A periodic clean task will delete least recently accessed images to maintain the size limit.
func (i *ImageManager) SetMaxOnDiskCacheSizeBytes(size int64) {
	i.maxOnDiskCacheSizeBytes = size
}

// GetCoverThumbnailFromCache returns the cover thumbnail for the given ID if it exists
// in the in-memory cache. Returns quickly, safe to call in UI threads.
func (i *ImageManager) GetCoverThumbnailFromCache(coverID string) (image.Image, bool) {
	img, err := i.thumbnailCache.GetExtendTTL(coverID, i.thumbnailCache.DefaultTTL)
	if err == nil && img != nil {
		return img, true
	}
	return nil, false
}

// GetCoverThumbnailAsync asynchronously fetches the cover image for the given ID,
// and invokes the callback on completion. It returns a context.CancelFunc which can be used to
// cancel the fetch. The callback will not be invoked if the fetch is cancelled before completion.
// The cancel func must be invoked to avoid resource leaks. Use GetCoverThumbnail if cancellation is not needed.
func (i *ImageManager) GetCoverThumbnailAsync(coverID string, cb func(image.Image, error)) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if im, ok := i.GetCoverThumbnailFromCache(coverID); ok {
			if ctx.Err() == nil {
				cb(im, nil)
			}
		} else {
			i.fetchAndCacheCoverFromDiskOrServer(ctx, coverID, i.thumbnailCache.DefaultTTL, cb)
		}
	}()
	return cancel
}

// GetCoverThumbnail is a synchronous, blocking function to fetch the image for a given coverID.
// Like most ImageManager calls, it should usually be called in a goroutine to not block UI loading.
func (i *ImageManager) GetCoverThumbnail(coverID string) (image.Image, error) {
	if im, ok := i.GetCoverThumbnailFromCache(coverID); ok {
		return im, nil
	}
	return i.fetchAndCacheCoverFromDiskOrServer(context.Background(), coverID, i.thumbnailCache.DefaultTTL, nil)
}

// GetFullSizeCoverArt fetches the full size cover image for the given coverID.
// It blocks until the fetch is complete.
func (i *ImageManager) GetFullSizeCoverArt(coverID string) (image.Image, error) {
	if i.cachedFullSizeCoverID == coverID {
		i.cachedFullSizeCoverAccessedAt = time.Now().UnixMilli()
		return i.cachedFullSizeCover, nil
	}
	return i.getFullSizeCoverArtFromServer(context.Background(), coverID, nil)
}

// GetCoverThumbnailAsync asynchronously fetches the cover image for the given ID,
// and invokes the callback on completion. It returns a context.CancelFunc which can be used to
// cancel the fetch. The callback will not be invoked if the fetch is cancelled before completion.
// The cancel func must be invoked to avoid resource leaks. Use GetCoverThumbnail if cancellation is not needed.
func (i *ImageManager) GetFullSizeCoverArtAsync(coverID string, cb func(image.Image, error)) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if i.cachedFullSizeCoverID == coverID {
			i.cachedFullSizeCoverAccessedAt = time.Now().UnixMilli()
			if ctx.Err() == nil {
				cb(i.cachedFullSizeCover, nil)
			}
		} else {
			i.getFullSizeCoverArtFromServer(ctx, coverID, cb)
		}
	}()
	return cancel
}

// GetCoverArtURL returns the URL for the locally cached cover thumbnail, if it exists.
func (i *ImageManager) GetCoverArtUrl(coverID string) (string, error) {
	path := i.filePathForCover(coverID)
	if _, err := os.Stat(path); err == nil {
		// this is probably broken for Windows but it's currently only used
		// for MPRIS, so we are OK for now
		return fmt.Sprintf("file://%s", path), nil
	}
	return "", errors.New("cover not found")
}

// GetCoverArtPath returns the file path for the locally cached cover thumbnail, if it exists.
func (i *ImageManager) GetCoverArtPath(coverID string) (string, error) {
	path := i.filePathForCover(coverID)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	return "", errors.New("cover not found")
}

// GetCachedArtistImage returns the artist image for the given artistID from the on-disc cache, if it exists.
func (i *ImageManager) GetCachedArtistImage(artistID string) (image.Image, bool) {
	return i.loadLocalImage(i.filePathForArtistImage(artistID))
}

// FetchAndCacheArtistImage fetches the artist image for the given artistID from the server,
// caching it locally if the fetch succeeds. Blocks until fetch is completed.
func (i *ImageManager) FetchAndCacheArtistImage(artistID string, imgURL string) (image.Image, error) {
	im, err := i.fetchRemoteArtistImage(imgURL)
	if err != nil {
		return nil, err
	}
	_ = i.writeJpeg(im, i.filePathForArtistImage(artistID))
	return im, nil
}

// RefreshCachedArtistImageIfExpired re-fetches the artist image from the server if expired.
func (i *ImageManager) RefreshCachedArtistImageIfExpired(artistID string, imgURL string) error {
	stat, err := os.Stat(i.filePathForArtistImage(artistID))
	if err == nil && time.Since(stat.ModTime()) > cachedImageValidTime {
		_, err = i.FetchAndCacheArtistImage(artistID, imgURL)
	}
	return err
}

// ImageCacheGeneration lets visible thumbnails detect an explicit cache clear.
func (i *ImageManager) ImageCacheGeneration() uint64 { return i.cacheGeneration.Load() }

// ClearInMemoryCache clears images from the in-memory caches.
func (i *ImageManager) ClearInMemoryCache() {
	i.cacheGeneration.Add(1)
	i.coverFlightsMu.Lock()
	defer i.coverFlightsMu.Unlock()
	for _, flight := range i.coverFlights {
		flight.cancel()
	}
	i.coverFlights = nil
	i.thumbnailCache.Clear()
	i.clearFullSizeCover()
}

func (i *ImageManager) ensureCoverCacheDir() string {
	// if user logged out with pending fetches in progress,
	// make sure we don't write to nil (00000000-*0) cache directory
	if i.s.ServerID == uuid.Nil {
		return ""
	}
	path := path.Join(i.baseCacheDir, i.s.ServerID.String(), "covers")
	configdir.MakePath(path)
	return path
}

func (i *ImageManager) ensureArtistCoverCacheDir() string {
	path := path.Join(i.baseCacheDir, i.s.ServerID.String(), "artistimages")
	configdir.MakePath(path)
	return path
}

func (i *ImageManager) fetchRemoteArtistImage(url string) (image.Image, error) {
	i.serverFetchSema <- struct{}{} // acquire
	res, err := fyne.LoadResourceFromURLString(url)
	<-i.serverFetchSema // release

	if err == nil {
		im, _, err := image.Decode(bytes.NewReader(res.Content()))
		if err == nil {
			return im, nil
		}
		return nil, err
	}
	return nil, err
}

func (i *ImageManager) fetchAndCacheCoverFromDiskOrServer(ctx context.Context, coverID string, ttl time.Duration, cb func(image.Image, error), sizes ...int) (image.Image, error) {
	size := coverArtThumbnailSize
	if len(sizes) > 0 {
		size = sizes[0]
	}
	generation := i.ImageCacheGeneration()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// on disc cache
	if i.ensureCoverCacheDir() != "" {
		path := i.filePathForCover(coverID, size)
		if s, err := os.Stat(path); err == nil {
			go i.checkRefreshLocalCover(s, coverID, ttl, size)
			if img, ok := i.loadLocalImage(path); ok {
				i.coverFlightsMu.Lock()
				valid := ctx.Err() == nil && generation == i.ImageCacheGeneration()
				if valid {
					i.thumbnailCache.SetWithTTL(coverCacheKey(coverID, size), img, ttl)
				}
				i.coverFlightsMu.Unlock()
				if !valid {
					return nil, context.Canceled
				}
				if ctx.Err() == nil && cb != nil {
					cb(img, nil)
				}
				return img, nil
			}
		}
	}

	// fetch from server
	return i.fetchAndCacheCoverFromServer(ctx, coverID, ttl, cb, size)
}

func (i *ImageManager) fetchAndCacheCoverFromServer(ctx context.Context, coverID string, ttl time.Duration, cb func(image.Image, error), sizes ...int) (image.Image, error) {
	size := coverArtThumbnailSize
	if len(sizes) > 0 {
		size = sizes[0]
	}
	img, err := i.sharedSizedCover(ctx, coverID, size, func(shared context.Context) (image.Image, error) {
		return i.fetchCoverFromServer(shared, coverID, ttl, size)
	})
	if ctx.Err() == nil && cb != nil {
		cb(img, err)
	}
	return img, err
}

func (i *ImageManager) fetchCoverFromServer(ctx context.Context, coverID string, ttl time.Duration, sizes ...int) (image.Image, error) {
	size := coverArtThumbnailSize
	if len(sizes) > 0 {
		size = sizes[0]
	}
	generation := i.ImageCacheGeneration()
	if i.s.Server == nil {
		err := errors.New("logged out")
		return nil, err
	}
	// Artist resolution must not occupy every slot needed by album covers.
	if classifier, ok := i.s.Server.(interface{ IsArtistCoverArt(string) bool }); ok && classifier.IsArtistCoverArt(coverID) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case i.artistFetchSema <- struct{}{}:
		}
		defer func() { <-i.artistFetchSema }()
	}
	select {
	case <-ctx.Done():
		return nil, context.Canceled
	case i.serverFetchSema <- struct{}{}: // acquire
		defer func() { <-i.serverFetchSema }()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if img, ok := i.GetCoverArtSizedFromCache(coverID, size); ok {
			return img, nil
		}
		server := i.s.Server
		if server == nil {
			return nil, errors.New("logged out")
		}
		img, err := fetchCoverWithContext(ctx, server, coverID, size)
		i.coverFlightsMu.Lock()
		if err == nil && ctx.Err() == nil && generation == i.ImageCacheGeneration() && imageIsCacheable(img) {
			if i.ensureCoverCacheDir() != "" {
				var options *jpeg.Options
				if size > coverArtThumbnailSize {
					options = &jpeg.Options{Quality: 95}
				}
				_ = i.writeJpegWithOptions(img, i.filePathForCover(coverID, size), options)
			}
			i.thumbnailCache.SetWithTTL(coverCacheKey(coverID, size), img, ttl)
		}
		i.coverFlightsMu.Unlock()
		return img, err
	}
}

func (i *ImageManager) getFullSizeCoverArtFromServer(ctx context.Context, coverID string, cb func(image.Image, error)) (image.Image, error) {
	if i.s.Server == nil {
		err := errors.New("logged out")
		if ctx.Err() == nil && cb != nil {
			cb(nil, err)
		}
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, context.Canceled
	case i.serverFetchSema <- struct{}{}: // acquire
		defer func() { <-i.serverFetchSema }()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		server := i.s.Server
		if server == nil {
			return nil, errors.New("logged out")
		}
		im, err := fetchCoverWithContext(ctx, server, coverID, 0)
		if err == nil && imageIsCacheable(im) {
			i.cachedFullSizeCover = im
			i.cachedFullSizeCoverID = coverID
			i.cachedFullSizeCoverAccessedAt = time.Now().UnixMilli()
		}
		if ctx.Err() == nil && cb != nil {
			cb(im, err)
		}
		return im, err
	}
}

func (i *ImageManager) checkRefreshLocalCover(stat os.FileInfo, coverID string, ttl time.Duration, sizes ...int) {
	size := coverArtThumbnailSize
	if len(sizes) > 0 {
		size = sizes[0]
	}
	if time.Since(stat.ModTime()) > cachedImageValidTime {
		i.fetchAndCacheCoverFromServer(context.Background(), coverID, ttl, nil, size)
	}
}

func (i *ImageManager) filePathForCover(coverID string, sizes ...int) string {
	size := coverArtThumbnailSize
	if len(sizes) > 0 {
		size = sizes[0]
	}
	if size != coverArtThumbnailSize {
		dir := filepath.Join(i.ensureCoverCacheDir(), fmt.Sprintf("size-%d", size))
		if i.ensureCoverCacheDir() != "" {
			_ = os.MkdirAll(dir, 0755)
		}
		return filepath.Join(dir, fmt.Sprintf("%s.jpg", sanitizeFileName(coverID)))
	}
	return filepath.Join(i.ensureCoverCacheDir(), fmt.Sprintf("%s.jpg", sanitizeFileName(coverID)))
}

func (i *ImageManager) filePathForArtistImage(id string) string {
	return filepath.Join(i.ensureArtistCoverCacheDir(), fmt.Sprintf("%s.jpg", sanitizeFileName(id)))
}

func (i *ImageManager) writeJpeg(img image.Image, path string) error {
	return i.writeJpegWithOptions(img, path, nil)
}

func (i *ImageManager) writeJpegWithOptions(img image.Image, path string, options *jpeg.Options) error {
	f, err := os.Create(path)
	if err == nil {
		defer f.Close()
		if err := jpeg.Encode(f, img, options); err != nil {
			log.Printf("failed to cache image: %s", err.Error())
			return err
		}
	}
	i.filesWrittenSinceLastPrune = true
	return err
}

func (i *ImageManager) loadLocalImage(path string) (image.Image, bool) {
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		if img, _, err := image.Decode(f); err == nil {
			return img, true
		}
	}
	return nil, false
}

func (i *ImageManager) clearFullSizeCoverIfExpired() {
	now := time.Now().UnixMilli()
	if now-i.cachedFullSizeCoverAccessedAt > fullSizeCoverExpires.Milliseconds() {
		i.clearFullSizeCover()
	}
}

func (i *ImageManager) clearFullSizeCover() {
	i.cachedFullSizeCoverID = ""
	i.cachedFullSizeCover = nil
}

func (im *ImageManager) pruneOnDiskCache() {
	if !im.filesWrittenSinceLastPrune {
		return // no new covers cached since last run, no need to walk dir
	}

	// collect list of all cached covers (across servers)
	// we use modTime as a proxy for last accessed time
	// since covers are refreshed from the server after a fixed interval,
	// modTime is roughly equivalent to last access
	type fileInfo struct {
		path    string
		size    int64
		modTime int64
	}
	var allCovers []fileInfo
	var totalSize int64
	filepath.WalkDir(im.baseCacheDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "jpg") {
			return nil
		}
		if info, err := d.Info(); err == nil {
			s := info.Size()
			allCovers = append(allCovers,
				fileInfo{path: path, size: s, modTime: info.ModTime().UnixMilli()})
			totalSize += s
		}

		return nil
	})

	if totalSize > im.maxOnDiskCacheSizeBytes {
		// sort and then delete from least recently modified until size is under threshold
		sort.Slice(allCovers, func(i, j int) bool {
			return allCovers[i].modTime < allCovers[j].modTime
		})
		for i := 0; i < len(allCovers) && totalSize > im.maxOnDiskCacheSizeBytes; i++ {
			if err := os.Remove(allCovers[i].path); err == nil {
				totalSize -= allCovers[i].size
			}
		}
	}
	im.filesWrittenSinceLastPrune = false
}

var illegalFilename = regexp.MustCompile("[" + regexp.QuoteMeta("`~!@#$%^&*+={}|/\\:;\"'<>?") + "]")

func sanitizeFileName(s string) string {
	name := illegalFilename.ReplaceAllString(s, "_")
	// Server-provided artist image URLs can exceed filesystem name limits.
	if strings.Contains(s, "://") || len(name) > 200 {
		return fmt.Sprintf("%x", sha256.Sum256([]byte(s)))
	}
	return name
}

// Honor provider cache policy for temporary images in both memory and on disk.
func imageIsCacheable(img image.Image) bool {
	if policy, ok := img.(interface{ Cacheable() bool }); ok {
		return policy.Cacheable()
	}
	return img != nil
}
