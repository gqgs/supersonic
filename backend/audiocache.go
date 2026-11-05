package backend

import (
	"context"
	"errors"
	"log"
	"os"
	"slices"
	"sync"

	"github.com/supersonic-app/supersonic/sharedutil"
)

// AudioCache manages temporary local storage of audio files fetched from the remote music server.
// It prefetches and stores tracks on disk based on an upcoming play queue.
type AudioCache struct {
	mutex sync.Mutex

	s            *ServerManager
	rootCtx      context.Context
	baseCacheDir string

	entries map[string]*cacheEntry
	workers sync.WaitGroup
	closed  bool
}

type cacheEntry struct {
	path            string
	done            bool // terminal, including failed or canceled downloads
	err             error
	refCount        int
	legacyRefs      int // ID-based references must pin their generation
	pendingDeletion bool
	cancel          context.CancelFunc
}

// cacheFileReference pins one download attempt, not just a track ID. A retry can
// replace the current entry without changing this reader's path or status.
type cacheFileReference struct {
	cache *AudioCache
	entry *cacheEntry
	once  sync.Once
}

func (r *cacheFileReference) status() (bool, error) {
	r.cache.mutex.Lock()
	defer r.cache.mutex.Unlock()
	return r.entry.done, r.entry.err
}

func (r *cacheFileReference) release() {
	r.once.Do(func() {
		r.cache.mutex.Lock()
		defer r.cache.mutex.Unlock()
		r.cache.releaseEntry(r.entry)
	})
}

// AudioCacheRequest represents a request to prefetch and cache an audio file.
type AudioCacheRequest struct {
	ID          string
	DownloadURL string
}

func NewAudioCache(ctx context.Context, s *ServerManager, baseCacheDir string) (*AudioCache, error) {
	if err := os.MkdirAll(baseCacheDir, 0700); err != nil {
		return nil, errors.New("failed to create audio cache dir")
	}
	return &AudioCache{
		s: s, rootCtx: ctx, baseCacheDir: baseCacheDir,
		entries: make(map[string]*cacheEntry),
	}, nil
}

// PathForCachedFile returns a path only after a successful complete download.
func (a *AudioCache) PathForCachedFile(id string) string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if e := a.entries[id]; e != nil && e.done && e.err == nil && !e.pendingDeletion {
		return e.path
	}
	return ""
}

func (a *AudioCache) IsFullyDownloaded(id string) bool {
	return a.PathForCachedFile(id) != ""
}

func (a *AudioCache) PathForCachedOrDownloadingFile(id string) string {
	return a.pathForCachedOrDownloadingFile(id, false)
}

// ObtainReferenceToFile pins the current generation until the matching
// ReleaseReferenceToFile. Internal consumers should use obtainFile instead,
// whose reference remains unambiguous across same-ID retries.
func (a *AudioCache) ObtainReferenceToFile(id string) string {
	return a.pathForCachedOrDownloadingFile(id, true)
}

func (a *AudioCache) pathForCachedOrDownloadingFile(id string, obtainReference bool) string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if e := a.entries[id]; e != nil && e.err == nil && !e.pendingDeletion {
		if obtainReference {
			e.refCount++
			e.legacyRefs++
		}
		return e.path
	}
	return ""
}

func (a *AudioCache) obtainFile(id string) (*cacheFileReference, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.closed {
		return nil, context.Canceled
	}
	if e := a.entries[id]; e != nil {
		if e.err != nil {
			return nil, e.err
		}
		if e.pendingDeletion {
			return nil, context.Canceled
		}
		e.refCount++
		return &cacheFileReference{cache: a, entry: e}, nil
	}
	return nil, nil
}

func (a *AudioCache) ReleaseReferenceToFile(id string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if e := a.entries[id]; e != nil && e.legacyRefs > 0 {
		e.legacyRefs--
		a.releaseEntry(e)
		if e.pendingDeletion && e.legacyRefs == 0 {
			delete(a.entries, id)
		}
	}
}

// Caller holds mutex. Removal waits for the writer as well as its readers.
func (a *AudioCache) releaseEntry(e *cacheEntry) {
	e.refCount--
	if e.refCount == 0 && e.done && (e.pendingDeletion || e.err != nil) {
		_ = os.Remove(e.path)
	}
}

// CacheFile starts a new attempt if absent or previously failed. Each attempt
// owns a unique path, so a canceled worker cannot overwrite/delete its retry.
func (a *AudioCache) CacheFile(id, dlURL string) {
	if a.s == nil || a.s.Server == nil {
		return
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.cacheFile(id, dlURL)
}

func (a *AudioCache) cacheFile(id, dlURL string) {
	if a.closed || a.rootCtx.Err() != nil {
		return
	}
	if old := a.entries[id]; old != nil {
		if old.legacyRefs > 0 || (!old.pendingDeletion && (!old.done || old.err == nil)) {
			return
		}
		a.deleteEntry(id, old)
	}

	ctx, cancel := context.WithCancel(a.rootCtx)
	e := &cacheEntry{cancel: cancel}
	a.entries[id] = e
	f, err := os.CreateTemp(a.baseCacheDir, "audio-*")
	if err != nil {
		e.done, e.err = true, errors.New("failed to create audio cache file")
		cancel()
		return
	}
	e.path = f.Name()
	if err := f.Close(); err != nil {
		e.done, e.err = true, errors.New("failed to close audio cache file")
		_ = os.Remove(e.path)
		cancel()
		return
	}

	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		defer cancel()
		ok, err := sharedutil.DownloadAudioFileWithContext(ctx, dlURL, e.path)
		if !ok && err == nil {
			err = errors.New("audio download did not complete")
		}
		if err == nil {
			err = ctx.Err()
		}
		a.mutex.Lock()
		e.done, e.err = true, err
		if err != nil || (e.pendingDeletion && e.refCount == 0) {
			_ = os.Remove(e.path)
		}
		a.mutex.Unlock()
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("error downloading audio file: %v", err)
		}
	}()
}

// CacheOnly retains requested tracks, cancels unused downloads, and removes
// their files after readers release them. Failed entries can be retried here.
func (a *AudioCache) CacheOnly(keep string, fetch []AudioCacheRequest) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.closed {
		return
	}
	for id, e := range a.entries {
		if id != keep && !slices.ContainsFunc(fetch, func(r AudioCacheRequest) bool { return r.ID == id }) {
			a.deleteEntry(id, e)
		}
	}
	for _, item := range fetch {
		a.cacheFile(item.ID, item.DownloadURL)
	}
}

func (a *AudioCache) Shutdown() {
	a.mutex.Lock()
	a.closed = true
	for id, e := range a.entries {
		a.deleteEntry(id, e)
	}
	a.mutex.Unlock()
	// Writers must finish before deleting the directory, including retired
	// generations that are no longer in entries.
	a.workers.Wait()
	if a.baseCacheDir != "" {
		_ = os.RemoveAll(a.baseCacheDir)
	}
}

func (a *AudioCache) deleteEntry(id string, e *cacheEntry) {
	e.pendingDeletion = true
	e.cancel()
	if e.legacyRefs == 0 && a.entries[id] == e {
		delete(a.entries, id)
	}
	if e.done && e.refCount == 0 {
		_ = os.Remove(e.path)
	}
}
