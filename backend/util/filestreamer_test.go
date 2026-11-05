package util

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func startFileStreamerTest(t *testing.T, ctx context.Context, data []byte, status func() (bool, error)) (*FileStreamerServer, <-chan error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audio")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := NewFileStreamerServerWithContext(ctx, path, status)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fs.Close)
	done := make(chan error, 1)
	go func() { done <- fs.Serve() }()
	return fs, done
}

func awaitFileStreamerTest(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("streamer did not stop")
		return nil
	}
}

func fileStreamerTestClient() *http.Client { return &http.Client{Timeout: 3 * time.Second} }

func TestFileStreamerCompleteFile(t *testing.T) {
	data := bytes.Repeat([]byte("audio payload"), 2048)
	fs, done := startFileStreamerTest(t, context.Background(), data, func() (bool, error) { return true, nil })
	resp, err := fileStreamerTestClient().Get(fs.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("completed input produced failed HTTP response: %v", err)
	}
	if resp.StatusCode != 200 || !bytes.Equal(got, data) {
		t.Fatalf("status=%d bytes=%d want=%d", resp.StatusCode, len(got), len(data))
	}
	if err := awaitFileStreamerTest(t, done); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestFileStreamerGrowingFile(t *testing.T) {
	initial := bytes.Repeat([]byte("a"), 8192)
	var complete atomic.Bool
	fs, done := startFileStreamerTest(t, context.Background(), initial, func() (bool, error) { return complete.Load(), nil })
	resp, err := fileStreamerTestClient().Get(fs.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	prefix := make([]byte, len(initial)-1024)
	if _, err := io.ReadFull(resp.Body, prefix); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prefix, initial[:len(prefix)]) {
		t.Fatal("incorrect growing prefix")
	}
	tail := bytes.Repeat([]byte("b"), 4096)
	file, err := os.OpenFile(fs.Path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write(tail)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("append: %v close: %v", err, closeErr)
	}
	complete.Store(true)
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("completed growing input produced failed HTTP response: %v", err)
	}
	if !bytes.Equal(append(prefix, rest...), append(initial, tail...)) {
		t.Fatal("growing content differs")
	}
	if err := awaitFileStreamerTest(t, done); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestFileStreamerFailedInput(t *testing.T) {
	t.Run("before request", func(t *testing.T) {
		fs, done := startFileStreamerTest(t, context.Background(), nil, func() (bool, error) { return true, errors.New("private upstream error") })
		resp, err := fileStreamerTestClient().Get(fs.Addr())
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadGateway || bytes.Contains(body, []byte("private")) {
			t.Fatalf("status=%d body=%q", resp.StatusCode, body)
		}
		awaitFileStreamerTest(t, done)
	})
	t.Run("during request", func(t *testing.T) {
		var failed atomic.Bool
		fs, done := startFileStreamerTest(t, context.Background(), bytes.Repeat([]byte("a"), 8192), func() (bool, error) {
			if failed.Load() {
				return true, errors.New("upstream failed")
			}
			return false, nil
		})
		resp, err := fileStreamerTestClient().Get(fs.Addr())
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		first := make([]byte, 8192-1024)
		if _, err := io.ReadFull(resp.Body, first); err != nil {
			t.Fatal(err)
		}
		failed.Store(true)
		_, err = io.ReadAll(resp.Body)
		if err == nil {
			t.Fatal("failed partial input was reported as successful EOF")
		}
		awaitFileStreamerTest(t, done)
	})
}

func TestFileStreamerCancellation(t *testing.T) {
	for _, source := range []bool{true, false} {
		name := "reader"
		if source {
			name = "source"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancelSource := context.WithCancel(context.Background())
			defer cancelSource()
			fs, done := startFileStreamerTest(t, ctx, bytes.Repeat([]byte("a"), 8192), func() (bool, error) { return false, nil })
			readerCtx, cancelReader := context.WithCancel(context.Background())
			defer cancelReader()
			req, err := http.NewRequestWithContext(readerCtx, http.MethodGet, fs.Addr(), nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := fileStreamerTestClient().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			first := make([]byte, 8192-1024)
			if _, err := io.ReadFull(resp.Body, first); err != nil {
				t.Fatal(err)
			}
			if source {
				cancelSource()
			} else {
				cancelReader()
			}
			_, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				t.Fatal("canceled partial input returned successful EOF")
			}
			awaitFileStreamerTest(t, done)
		})
	}
}

func TestFileStreamerCloseWithoutRequest(t *testing.T) {
	for _, serve := range []bool{false, true} {
		name := "before Serve"
		if serve {
			name = "while serving"
		}
		t.Run(name, func(t *testing.T) {
			fs, err := NewFileStreamerServer("unused", func() bool { return false })
			if err != nil {
				t.Fatal(err)
			}
			addr := fs.listener.Addr().String()
			var done chan error
			if serve {
				done = make(chan error, 1)
				go func() { done <- fs.Serve() }()
			}
			fs.Close()
			fs.Close()
			if serve {
				awaitFileStreamerTest(t, done)
			}
			listener, err := net.Listen("tcp", addr)
			if err != nil {
				t.Fatalf("listener not released: %v", err)
			}
			listener.Close()
		})
	}
}

func TestFileStreamerMultipleHandlers(t *testing.T) {
	data := []byte("complete audio")
	path := filepath.Join(t.TempDir(), "audio")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	fs, err := NewFileStreamerServer(path, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	// Keep the HTTP server alive to exercise simultaneous handler completion,
	// independently of the single-reader Serve shutdown policy.
	server := httptest.NewServer(http.HandlerFunc(fs.streamHandler))
	defer server.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := fileStreamerTestClient().Get(server.URL)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			got, err := io.ReadAll(resp.Body)
			if err != nil || !bytes.Equal(got, data) {
				t.Errorf("read=%q err=%v", got, err)
			}
		}()
	}
	wg.Wait()
	select {
	case <-fs.done:
	default:
		t.Fatal("completion was not signaled")
	}
}
