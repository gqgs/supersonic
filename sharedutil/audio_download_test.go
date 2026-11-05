package sharedutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadAudioResponses(t *testing.T) {
	for _, tt := range []struct {
		name, body, mime string
		status           int
		good             bool
	}{
		{"XML", `<subsonic-response status="failed"><error message="secret"/></subsonic-response>`, "application/octet-stream", 200, false},
		{"JSON", ` {"error":"secret"}`, "audio/flac", 200, false},
		{"HTML", "\xef\xbb\xbf  <!doctype html><html>secret</html>", "application/octet-stream", 200, false},
		{"plain", "Upstream download failed: secret", "application/octet-stream", 200, false},
		{"unicode text", "无法下载音频", "audio/mpeg", 200, false},
		{"whitespace", strings.Repeat(" \t\r\n", 1000) + "<error/>", "audio/mpeg", 200, false},
		{"UTF16", "\xff\xfe<\x00e\x00r\x00r\x00o\x00r\x00", "application/octet-stream", 200, false},
		{"empty", "", "audio/flac", 200, false},
		{"unauthorized", "secret", "audio/flac", 401, false},
		{"server error", "secret", "audio/flac", 503, false},
		{"partial status", "fLaCdata", "audio/flac", 206, false},
		{"FLAC", "fLaCmetadata", "text/plain", 200, true},
		{"MP3 ID3", "ID3metadata", "application/json", 200, true},
		{"MP3 frames", "\xff\xfb\x90\x00payload", "application/octet-stream", 200, true},
		{"MPEG layer I BOM-like", "\xff\xfe\x90\x00\x12\x34", "text/plain", 200, true},
		{"unknown angle binary", "<\x00\x01binary", "application/octet-stream", 200, true},
		{"unknown brace binary", "{\x00\x01binary", "application/octet-stream", 200, true},
		{"unknown bracket binary", "[\x00\x01binary", "application/octet-stream", 200, true},
		{"MPEG layer I", "\xff\xfe\x90\x44payload", "text/plain", 200, true},
		{"AAC", "\xff\xf1\x50\x80payload", "text/html", 200, true},
		{"EAC3", "\x0b\x77\x00\x00payload", "application/octet-stream", 200, true},
		{"ALAC M4A", "\x00\x00\x00\x20ftypM4A \x00\x00\x00\x00", "text/plain", 200, true},
		{"OGG", "OggSmetadata", "application/octet-stream", 200, true},
		{"WAV", "RIFFmetadata", "application/octet-stream", 200, true},
		{"unknown binary", "\x01\x02\x03\x04something", "text/plain", 200, true},
		{"large binary", "fLaC" + strings.Repeat("\x00\x01", 2048), "audio/flac", 200, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.mime)
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "audio-atmos")
			ok, err := DownloadAudioFileWithContext(context.Background(), srv.URL+"/?token=secret", path)
			if ok != tt.good || (err == nil) != tt.good {
				t.Fatalf("ok=%v err=%v; want good=%v", ok, err, tt.good)
			}
			if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), srv.URL)) {
				t.Fatalf("unsafe error: %v", err)
			}
			data, readErr := os.ReadFile(path)
			if tt.good {
				if readErr != nil || !bytes.Equal(data, []byte(tt.body)) {
					t.Fatalf("incorrect file: %v", readErr)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("failed download left file: %v", readErr)
			}
		})
	}
}

func TestDownloadAudioTruncated(t *testing.T) {
	for _, size := range []int{20, 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", fmt.Sprint(size+100))
				fmt.Fprint(w, "fLaC"+strings.Repeat("x", size-4))
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "partial")
			ok, err := DownloadAudioFileWithContext(context.Background(), srv.URL, path)
			if ok || err == nil || !strings.Contains(err.Error(), "truncated") {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("partial remains: %v", err)
			}
		})
	}
}

func TestDownloadAudioCancellation(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			started := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, "fLaC"+strings.Repeat("x", 1024))
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := context.WithCancel(context.Background())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				want = context.DeadlineExceeded
			}
			defer cancel()
			path := filepath.Join(t.TempDir(), "partial")
			if !deadline {
				go func() { <-started; cancel() }()
			}
			ok, err := DownloadAudioFileWithContext(ctx, srv.URL+"/?token=secret", path)
			if ok || !errors.Is(err, want) {
				t.Fatalf("ok=%v err=%v; want %v", ok, err, want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe error: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("partial remains: %v", err)
			}
		})
	}
}

func TestDownloadAudioRequestErrorsArePrivate(t *testing.T) {
	for _, url := range []string{"http://user:secret@127.0.0.1:1/?token=secret", "http://secret%gh"} {
		ok, err := DownloadAudioFileWithContext(context.Background(), url, filepath.Join(t.TempDir(), "audio"))
		if ok || err == nil {
			t.Fatal("expected failure")
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), url) {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestDownloadAudioCreateFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "fLaCdata") }))
	defer srv.Close()
	ok, err := DownloadAudioFileWithContext(context.Background(), srv.URL, filepath.Join(t.TempDir(), "missing", "audio"))
	if ok || err == nil {
		t.Fatal("expected file creation failure")
	}
}

func TestDownloadAudioTruncatedChunked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n20\r\nfLaCshort")
		rw.Flush()
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "partial")
	ok, err := DownloadAudioFileWithContext(context.Background(), srv.URL, path)
	if ok || err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial remains: %v", err)
	}
}

// Generate real containers/frames when ffmpeg is available. Tests remain usable
// on developer machines without ffmpeg; the synthetic cases above always run.
func TestDownloadAudioFFmpegFormats(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	for _, tt := range []struct{ codec, ext string }{
		{"alac", "m4a"}, {"aac", "aac"}, {"eac3", "eac3"}, {"flac", "flac"},
	} {
		t.Run(tt.codec, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "sample."+tt.ext)
			cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=0.1", "-c:a", tt.codec, source)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generate audio: %v: %s", err, output)
			}
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.Write(data)
			}))
			defer srv.Close()
			dest := filepath.Join(t.TempDir(), "track-atmos")
			ok, err := DownloadAudioFileWithContext(context.Background(), srv.URL, dest)
			if !ok || err != nil {
				t.Fatalf("valid %s download: %v", tt.codec, err)
			}
			result, err := os.ReadFile(dest)
			if err != nil || !bytes.Equal(data, result) {
				t.Fatalf("download differs from generated %s: %v", tt.codec, err)
			}
		})
	}
}
