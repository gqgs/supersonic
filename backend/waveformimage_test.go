package backend

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/supersonic-app/go-mpv"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

func testWaveformWAV() []byte {
	// One second of mono PCM, so the test does not depend on ffmpeg.
	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(36+44100))
	out.WriteString("WAVEfmt ")
	_ = binary.Write(&out, binary.LittleEndian, uint32(16))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint32(22050))
	_ = binary.Write(&out, binary.LittleEndian, uint32(44100))
	_ = binary.Write(&out, binary.LittleEndian, uint16(2))
	_ = binary.Write(&out, binary.LittleEndian, uint16(16))
	out.WriteString("data")
	_ = binary.Write(&out, binary.LittleEndian, uint32(44100))
	for i := range 22050 {
		_ = binary.Write(&out, binary.LittleEndian, int16((i%64-32)*500))
	}
	return out.Bytes()
}

func waitWaveformStopped(t *testing.T, job *WaveformImageJob) {
	t.Helper()
	waitAudioCondition(t, func() bool {
		job.Get() // exercise UI snapshots while workers write the image
		_ = job.Done()
		_ = job.Err()
		_ = job.Canceled()
		job.lock.Lock()
		defer job.lock.Unlock()
		return job.done
	})
}

func TestWaveformRejectsFailedDownloadAndReleasesReferences(t *testing.T) {
	for _, response := range []string{"xml", "empty", "truncated"} {
		t.Run(response, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				switch response {
				case "xml":
					_, _ = io.WriteString(w, `<subsonic-response status="failed"/>`)
				case "truncated":
					w.Header().Set("Content-Length", "999999")
					_, _ = w.Write(testWaveformWAV()[:512])
				}
			}))
			defer srv.Close()
			a := newTestAudioCache(t)
			g := NewWaveformImageGenerator(a)
			defer g.Close()
			a.CacheOnly("", []AudioCacheRequest{{"bad", srv.URL}})
			job := g.StartWaveformGeneration(&mediaprovider.Track{ID: "bad", Duration: time.Second})
			waitWaveformStopped(t, job)
			if job.Err() == nil {
				t.Fatal("failed input produced successful waveform")
			}
			e := waitAudioEntry(t, a, "bad")
			a.mutex.Lock()
			refs := e.refCount
			a.mutex.Unlock()
			if refs != 0 {
				t.Fatalf("leaked %d references", refs)
			}
		})
	}
}

func TestWaveformRepeatedConversionsAndCancellation(t *testing.T) {
	data := testWaveformWAV()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	defer srv.Close()
	a := newTestAudioCache(t)
	g := NewWaveformImageGenerator(a)
	defer g.Close()
	a.CacheOnly("", []AudioCacheRequest{{"tone-atmos", srv.URL}})
	e := waitAudioEntry(t, a, "tone-atmos")
	for i := range 4 {
		job := g.StartWaveformGeneration(&mediaprovider.Track{ID: "tone-atmos", Duration: time.Second})
		if i == 1 {
			job.Cancel()
		}
		waitWaveformStopped(t, job)
		if i == 1 {
			if !errors.Is(job.Err(), context.Canceled) {
				t.Fatalf("canceled conversion returned %v", job.Err())
			}
		} else if job.Err() != nil {
			t.Fatalf("conversion %d failed: %v", i, job.Err())
		}
		a.mutex.Lock()
		refs := e.refCount
		a.mutex.Unlock()
		if refs != 0 {
			t.Fatalf("conversion %d leaked %d references", i, refs)
		}
		files, err := filepath.Glob(filepath.Join(a.baseCacheDir, "waveform-*.wav"))
		if err != nil || len(files) != 0 {
			t.Fatalf("conversion left temporary files: %v %v", files, err)
		}
	}
}

func TestWaveformConversionRejectsMissingOutput(t *testing.T) {
	a := newTestAudioCache(t)
	g := NewWaveformImageGenerator(a)
	defer g.Close()
	input := filepath.Join(t.TempDir(), "invalid-audio")
	if err := os.WriteFile(input, []byte("not playable audio"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := g.convertToWav(ctx, "bad", input, filepath.Join(t.TempDir(), "out.wav"))
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invalid conversion did not fail promptly: %v", err)
	}
	for _, data := range [][]byte{nil, make([]byte, 44)} {
		path := filepath.Join(t.TempDir(), "output.wav")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if validateWaveformOutput(path) == nil {
			t.Fatal("missing samples accepted")
		}
	}
}

func TestWaveformQueuedCancellationAndClose(t *testing.T) {
	a := newTestAudioCache(t)
	g := NewWaveformImageGenerator(a)
	// Hold the conversion slot, as a slow preceding job would, without
	// relying on scheduling or a decoder/network timeout.
	g.convertGate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.convertToWav(ctx, "queued", "unused", "unused") }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancellation waited for active converter")
	}
	queued := make(chan error, 1)
	go func() { queued <- g.convertToWav(context.Background(), "queued", "unused", "unused") }()
	closed := make(chan struct{})
	go func() { g.Close(); close(closed) }()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel queued conversion")
	}
	<-g.convertGate
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	g.Close()
}

func TestWaveformCanceledWhileWaitingForInput(t *testing.T) {
	a := newTestAudioCache(t)
	g := NewWaveformImageGenerator(a)
	defer g.Close()
	job := g.StartWaveformGeneration(&mediaprovider.Track{ID: "not-yet-downloading", Duration: time.Second})
	job.Cancel()
	waitWaveformStopped(t, job)
	if !errors.Is(job.Err(), context.Canceled) {
		t.Fatalf("unexpected error: %v", job.Err())
	}
}

func TestWaveformMalformedThenValid(t *testing.T) {
	garbage := bytes.Repeat([]byte{0x00, 0x81, 0x11, 0x00, 0x91}, 1024)
	good := testWaveformWAV()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			_, _ = w.Write(garbage)
		} else {
			_, _ = w.Write(good)
		}
	}))
	defer srv.Close()
	a := newTestAudioCache(t)
	g := NewWaveformImageGenerator(a)
	defer g.Close()
	for _, name := range []string{"bad", "good"} {
		a.CacheOnly("", []AudioCacheRequest{{name, srv.URL + "/" + name}})
		e := waitAudioEntry(t, a, name)
		if e.err != nil {
			t.Fatalf("binary was rejected before MPV: %v", e.err)
		}
		job := g.StartWaveformGeneration(&mediaprovider.Track{ID: name, Duration: time.Second})
		waitWaveformStopped(t, job)
		if name == "bad" && job.Err() == nil {
			t.Fatal("invalid binary produced successful waveform")
		}
		if name == "good" && job.Err() != nil {
			t.Fatalf("valid conversion after failure: %v", job.Err())
		}
		a.mutex.Lock()
		refs := e.refCount
		a.mutex.Unlock()
		if refs != 0 {
			t.Fatalf("conversion leaked %d references", refs)
		}
	}
}

func TestWaveformActiveCancellationThenValid(t *testing.T) {
	data := testWaveformWAV()
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseInput := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseInput()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data[:8192])
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(data[8192:])
	}))
	defer func() { releaseInput(); srv.Close() }()
	a := newTestAudioCache(t)
	t.Cleanup(releaseInput)
	g := NewWaveformImageGenerator(a)
	defer g.Close()
	a.CacheOnly("", []AudioCacheRequest{{"slow", srv.URL}})
	job := g.StartWaveformGeneration(&mediaprovider.Track{ID: "slow", Duration: time.Second})
	waitAudioCondition(t, func() bool { return len(g.convertGate) == 1 })
	job.Cancel()
	waitWaveformStopped(t, job)
	if !errors.Is(job.Err(), context.Canceled) {
		t.Fatalf("active cancellation: %v", job.Err())
	}
	releaseInput()
	waitAudioEntry(t, a, "slow")
	job = g.StartWaveformGeneration(&mediaprovider.Track{ID: "slow", Duration: time.Second})
	waitWaveformStopped(t, job)
	if job.Err() != nil {
		t.Fatalf("conversion after active cancellation: %v", job.Err())
	}
}

type waveformTestEvents struct {
	events []*mpv.Event
	index  int
}

func (m *waveformTestEvents) WaitEvent(float32) *mpv.Event {
	if m.index >= len(m.events) {
		return &mpv.Event{Event_Id: mpv.EVENT_NONE}
	}
	e := m.events[m.index]
	m.index++
	return e
}
func (*waveformTestEvents) GetPropertyString(string) string { return "yes" }
func (*waveformTestEvents) Command([]string) error          { return nil }

func TestWaveformPartialOutputDoesNotHideEndError(t *testing.T) {
	a := newTestAudioCache(t)
	g := NewWaveformImageGenerator(a)
	defer g.Close()
	path := filepath.Join(t.TempDir(), "partial.wav")
	if err := os.WriteFile(path, testWaveformWAV(), 0600); err != nil {
		t.Fatal(err)
	}
	// idle-active can already read "yes" while the error END_FILE event is
	// still queued. A nonempty output must not bypass that event.
	for _, fail := range []bool{true, false} {
		var endErr error
		if fail {
			endErr = errors.New("private decoder details")
		}
		events := &waveformTestEvents{events: []*mpv.Event{
			{Event_Id: mpv.EVENT_START_FILE},
			{Event_Id: mpv.EVENT_NONE},
			{Event_Id: mpv.EVENT_END_FILE, Error: endErr},
			{Event_Id: mpv.EVENT_IDLE},
		}}
		err := g.waitForWaveformConversion(context.Background(), events, path)
		if fail && (err == nil || err.Error() != "waveform audio decoding failed") {
			t.Fatalf("failed partial conversion returned %v", err)
		}
		if !fail && err != nil {
			t.Fatalf("clean completion failed: %v", err)
		}
		if events.index < 3 {
			t.Fatal("completion skipped the end-file event")
		}
	}
}

func TestWaveformFailedJobCanBeRetried(t *testing.T) {
	job := &WaveformImageJob{ItemID: "retry", img: NewWaveformImage()}
	p := &PlaybackManager{}
	p.wfmImageJobs[0] = job
	if found, ok := p.findWfmImageJob("retry", true); !ok || found != job {
		t.Fatal("healthy job was not reused")
	}
	job.setError(errors.New("failed download"))
	if _, ok := p.findWfmImageJob("retry", true); ok {
		t.Fatal("failed job blocked a fresh waveform attempt")
	}
	if _, ok := p.findWfmImageJob("retry", false); !ok {
		t.Fatal("failed job cannot be found for cleanup")
	}
}

func TestWaveformIgnoresPreStartEndEvents(t *testing.T) {
	a := newTestAudioCache(t)
	g := NewWaveformImageGenerator(a)
	defer g.Close()
	path := filepath.Join(t.TempDir(), "output.wav")
	if err := os.WriteFile(path, testWaveformWAV(), 0600); err != nil {
		t.Fatal(err)
	}
	events := &waveformTestEvents{events: []*mpv.Event{
		{Event_Id: mpv.EVENT_END_FILE, Error: errors.New("old canceled load")},
		{Event_Id: mpv.EVENT_IDLE},
		{Event_Id: mpv.EVENT_START_FILE},
		{Event_Id: mpv.EVENT_NONE},
		{Event_Id: mpv.EVENT_END_FILE},
		{Event_Id: mpv.EVENT_IDLE},
	}}
	if err := g.waitForWaveformConversion(context.Background(), events, path); err != nil {
		t.Fatalf("stale event failed new job: %v", err)
	}
	if events.index < 5 {
		t.Fatal("stale end event completed new job prematurely")
	}
}

func TestWaveformRejectsEmptyExtendedWAV(t *testing.T) {
	base := testWaveformWAV()
	header := append([]byte(nil), base[:36]...)
	binary.LittleEndian.PutUint32(header[4:8], 38)
	binary.LittleEndian.PutUint32(header[16:20], 18)
	header = append(header, 0, 0) // extended fmt chunk, but no PCM samples
	header = append(header, base[36:44]...)
	binary.LittleEndian.PutUint32(header[42:46], 0)
	path := filepath.Join(t.TempDir(), "empty-extended.wav")
	if err := os.WriteFile(path, header, 0600); err != nil {
		t.Fatal(err)
	}
	if validateWaveformOutput(path) != nil {
		t.Fatal("fixture must get past the file-size guard")
	}
	data := &waveformData{notify: make(chan struct{}, 1)}
	if err := analyzeWavFile(context.Background(), path, data, 1000, func() bool { return true }); err == nil {
		t.Fatal("header-only WAV was accepted as a successful waveform")
	}
}
