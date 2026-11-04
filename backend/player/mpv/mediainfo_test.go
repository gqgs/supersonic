package mpv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	api "github.com/supersonic-app/go-mpv"
)

func TestMediaInfoUninitializedIsNotUnavailable(t *testing.T) {
	_, err := New().GetMediaInfo()
	if !errors.Is(err, ErrUnitialized) || errors.Is(err, ErrMediaInfoUnavailable) {
		t.Fatalf("unexpected uninitialized error: %v", err)
	}
}

func TestMediaInfoPreservesUnexpectedMPVError(t *testing.T) {
	m := api.Create()
	defer m.TerminateDestroy()
	// Simulate a property read rejected by the backend itself, rather than our
	// initialization guard. Only PROPERTY_UNAVAILABLE is an expected transient.
	p := &Player{mpv: m, initialized: true}
	_, err := p.GetMediaInfo()
	if !errors.Is(err, api.ERROR_UNINITIALIZED) || errors.Is(err, ErrMediaInfoUnavailable) {
		t.Fatalf("unexpected mpv error was hidden or reclassified: %v", err)
	}
}

func TestMediaInfoLoadingAndRecovery(t *testing.T) {
	// Hold each response until the assertions have polled a genuinely loading
	// libmpv instance. No network timing assumptions or audio hardware are needed.
	requested := make(chan struct{}, 1)
	release := make(chan struct{})
	abort := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requested <- struct{}{}:
		case <-abort:
			return
		case <-r.Context().Done():
			return
		}
		select {
		case <-release:
		case <-abort:
			return
		case <-r.Context().Done():
			return
		}
		rate := uint32(44100)
		if r.URL.Path == "/second.wav" {
			rate = 48000
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(mediaInfoWAV(rate))
	}))
	defer server.Close()

	m := api.Create()
	defer m.TerminateDestroy()
	defer close(abort) // release the HTTP handler even if an assertion fails
	for key, value := range map[string]string{"ao": "null", "video": "no", "idle": "yes", "pause": "yes", "config": "no"} {
		if err := m.SetOptionString(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Initialize(); err != nil {
		t.Fatal(err)
	}
	p := &Player{mpv: m, initialized: true}
	assertUnavailable := func() {
		t.Helper()
		info, err := p.GetMediaInfo()
		if !errors.Is(err, ErrMediaInfoUnavailable) || !errors.Is(err, api.ERROR_PROPERTY_UNAVAILABLE) {
			t.Fatalf("expected unavailable metadata with original mpv error, got %+v, %v", info, err)
		}
		if info != (MediaInfo{}) {
			t.Fatalf("unavailable metadata contains stale/partial values: %+v", info)
		}
	}
	assertUnavailable() // idle, before any track has loaded

	for _, track := range []struct {
		path string
		rate int
	}{{"/first.wav", 44100}, {"/second.wav", 48000}} {
		if err := m.Command([]string{"loadfile", server.URL + track.path}); err != nil {
			t.Fatal(err)
		}
		select {
		case <-requested:
		case <-time.After(5 * time.Second):
			t.Fatal("mpv did not request the test track")
		}
		for range 20 {
			assertUnavailable()
		}
		select {
		case release <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("mpv abandoned the test track")
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			info, err := p.GetMediaInfo()
			if err == nil && info.Samplerate == track.rate && info.OutputSamplerate == track.rate && info.Codec != "" {
				break
			}
			if err != nil && !errors.Is(err, ErrMediaInfoUnavailable) {
				t.Fatalf("unexpected load error: %v", err)
			}
			if time.Now().After(deadline) {
				t.Fatalf("metadata did not recover for %s: %+v, %v", track.path, info, err)
			}
			m.WaitEvent(0.01)
		}
	}

	if err := m.Command([]string{"stop"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := p.GetMediaInfo()
		if errors.Is(err, ErrMediaInfoUnavailable) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("metadata did not clear after stop: %v", err)
		}
		m.WaitEvent(0.01)
	}
	assertUnavailable()
}

func mediaInfoWAV(rate uint32) []byte {
	// One second of silent mono, 16-bit PCM, generated without an ffmpeg dependency.
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36)+rate*2)
	wav.WriteString("WAVEfmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, rate)
	_ = binary.Write(&wav, binary.LittleEndian, rate*2)
	_ = binary.Write(&wav, binary.LittleEndian, uint16(2))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, rate*2)
	wav.Write(make([]byte, rate*2))
	return wav.Bytes()
}
