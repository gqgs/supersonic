package backend

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestAudioCache(t *testing.T) *AudioCache {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a, err := NewAudioCache(ctx, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); a.Shutdown() })
	return a
}

func waitAudioCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for audio worker")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitAudioEntry(t *testing.T, a *AudioCache, id string) *cacheEntry {
	t.Helper()
	var e *cacheEntry
	waitAudioCondition(t, func() bool {
		a.mutex.Lock()
		defer a.mutex.Unlock()
		e = a.entries[id]
		return e != nil && e.done
	})
	return e
}

func TestAudioCacheRejectsFailuresAndRetriesSameProfile(t *testing.T) {
	const id = "track-atmos.m4a"
	audio := append([]byte("fLaC"), make([]byte, 2048)...)
	for _, failure := range []string{"xml", "json", "http", "truncated"} {
		t.Run(failure, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				if r.URL.Path == "/good" {
					_, _ = w.Write(audio)
					return
				}
				switch failure {
				case "xml":
					_, _ = io.WriteString(w, `<?xml version="1.0"?><subsonic-response status="failed"><error message="private"/></subsonic-response>`)
				case "json":
					_, _ = io.WriteString(w, `{"subsonic-response":{"status":"failed"}}`)
				case "http":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "truncated":
					w.Header().Set("Content-Length", "9999")
					_, _ = w.Write(audio)
				}
			}))
			defer srv.Close()
			a := newTestAudioCache(t)
			a.CacheOnly("", []AudioCacheRequest{{id, srv.URL + "/bad"}})
			old := waitAudioEntry(t, a, id)
			if old.err == nil || a.IsFullyDownloaded(id) || a.PathForCachedOrDownloadingFile(id) != "" {
				t.Fatal("failed response exposed as cached audio")
			}
			if _, err := os.Stat(old.path); !os.IsNotExist(err) {
				t.Fatalf("failed file remains: %v", err)
			}
			if _, err := a.obtainFile(id); err == nil {
				t.Fatal("failure not propagated to reader")
			}
			a.CacheOnly("", []AudioCacheRequest{{id, srv.URL + "/good"}})
			new := waitAudioEntry(t, a, id)
			if new == old || new.path == old.path || new.err != nil {
				t.Fatalf("retry did not create an independent successful generation: %v", new.err)
			}
			got, err := os.ReadFile(a.PathForCachedFile(id))
			if err != nil || !bytes.Equal(got, audio) {
				t.Fatalf("fallback audio changed: %v", err)
			}
		})
	}
}

type audioRoundTripper func(*http.Request) (*http.Response, error)

func (f audioRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type delayedAudioBody struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	reader  *bytes.Reader
}

func (b *delayedAudioBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.release })
	return b.reader.Read(p)
}
func (*delayedAudioBody) Close() error { return nil }

func TestAudioCacheCanceledGenerationCannotCorruptRetry(t *testing.T) {
	oldBody := &delayedAudioBody{started: make(chan struct{}), release: make(chan struct{}), reader: bytes.NewReader(append([]byte("fLaC"), make([]byte, 4096)...))}
	var releaseOnce sync.Once
	releaseOld := func() { releaseOnce.Do(func() { close(oldBody.release) }) }
	defer releaseOld()
	fresh := append([]byte("fLaC"), bytes.Repeat([]byte{0x91}, 4096)...)
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: audioRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := io.ReadCloser(io.NopCloser(bytes.NewReader(fresh)))
		if r.URL.Path == "/old" {
			body = oldBody
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, ContentLength: -1}, nil
	})}
	defer func() { http.DefaultClient = previous }()
	a := newTestAudioCache(t)
	// Ensure the deliberately uncooperative transport is unblocked before cleanup.
	t.Cleanup(releaseOld)
	const id = "same-atmos-profile"
	a.CacheOnly("", []AudioCacheRequest{{id, "http://audio.test/old"}})
	<-oldBody.started
	old, err := a.obtainFile(id)
	if err != nil || old == nil {
		t.Fatalf("missing old reference: %v", err)
	}
	a.CacheOnly("", nil)
	a.CacheOnly("", []AudioCacheRequest{{id, "http://audio.test/new"}})
	current := waitAudioEntry(t, a, id)
	ref, err := a.obtainFile(id)
	if err != nil || ref == nil {
		t.Fatalf("missing retry reference: %v", err)
	}
	defer ref.release()
	if current.path == old.entry.path {
		t.Fatal("retry reused old writer's path")
	}
	releaseOld()
	waitAudioCondition(t, func() bool { done, _ := old.status(); return done })
	if _, err := old.status(); !errors.Is(err, context.Canceled) {
		t.Fatalf("old attempt did not retain cancellation: %v", err)
	}
	old.release()
	old.release() // releases are idempotent
	a.mutex.Lock()
	refs, same, err := current.refCount, a.entries[id] == current, current.err
	a.mutex.Unlock()
	if refs != 1 || !same || err != nil {
		t.Fatalf("old worker/reference changed retry: refs=%d same=%v err=%v", refs, same, err)
	}
	got, err := os.ReadFile(current.path)
	if err != nil || !bytes.Equal(got, fresh) {
		t.Fatalf("retry file corrupted by old cleanup: %v", err)
	}
	if _, err := os.Stat(old.entry.path); !os.IsNotExist(err) {
		t.Fatalf("old file was not cleaned up: %v", err)
	}
}

func TestAudioCacheLegacyReferencePinsGeneration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "fLaC\x00\x00\x00\x00") }))
	defer srv.Close()
	a := newTestAudioCache(t)
	a.CacheOnly("", []AudioCacheRequest{{"same", srv.URL}})
	old := waitAudioEntry(t, a, "same")
	if a.ObtainReferenceToFile("same") != old.path {
		t.Fatal("reference did not pin cached file")
	}
	a.CacheOnly("", nil)
	a.CacheOnly("", []AudioCacheRequest{{"same", srv.URL}})
	a.mutex.Lock()
	same := a.entries["same"] == old
	a.mutex.Unlock()
	if !same {
		t.Fatal("legacy ID reference became ambiguous")
	}
	a.ReleaseReferenceToFile("same")
	a.ReleaseReferenceToFile("same")
	a.CacheOnly("", []AudioCacheRequest{{"same", srv.URL}})
	fresh := waitAudioEntry(t, a, "same")
	if fresh == old || strings.EqualFold(fresh.path, old.path) {
		t.Fatal("new generation not created after legacy release")
	}
}

func TestAudioCacheLastReferenceRetriesFailedFileRemoval(t *testing.T) {
	a := newTestAudioCache(t)
	path := a.baseCacheDir + "/failed-file"
	if err := os.WriteFile(path, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	// Models a failed download whose initial remove was blocked by an open
	// decoder handle on Windows. The last reader must retry that removal.
	e := &cacheEntry{path: path, done: true, err: errors.New("failed"), refCount: 1}
	ref := &cacheFileReference{cache: a, entry: e}
	ref.release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed input remains after last reference: %v", err)
	}
}
