package browsing

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/supersonic-app/supersonic/backend/player/mpv"
)

func TestNowPlayingMediaInfoLoadingAndRecovery(t *testing.T) {
	var logs bytes.Buffer
	original := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(original) })

	info := mpv.MediaInfo{Codec: "flac", Bitrate: 900000, OutputFormat: "s16", OutputSamplerate: 44100}
	for range 2 {
		for range 20 {
			// Even if a previous or partial result is passed, do not display it
			// during loading. Wrapping must not change the error classification.
			if got := formatMediaInfo(info, fmt.Errorf("loading: %w", mpv.ErrMediaInfoUnavailable)); got != "" {
				t.Fatalf("loading displays stale metadata: %q", got)
			}
		}
		if logs.Len() != 0 {
			t.Fatalf("loading logged errors: %s", &logs)
		}
		got := formatMediaInfo(info, nil)
		if !strings.Contains(got, "FLAC, 900 kbps") || !strings.Contains(got, "PCM (s16) 44.1 kHz") {
			t.Fatalf("metadata did not recover: %q", got)
		}
	}
}

func TestNowPlayingMediaInfoStillLogsRealErrors(t *testing.T) {
	var logs bytes.Buffer
	original := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(original) })

	for _, err := range []error{
		mpv.ErrUnitialized,
		errors.New("unexpected media property failure"),
		// A matching error message alone is not a transient classification.
		errors.New(mpv.ErrMediaInfoUnavailable.Error()),
	} {
		logs.Reset()
		if got := formatMediaInfo(mpv.MediaInfo{}, err); got != "" {
			t.Fatalf("error displays metadata: %q", got)
		}
		if !strings.Contains(logs.String(), "error getting playback media info: "+err.Error()) {
			t.Fatalf("real error not logged: %v; log: %s", err, &logs)
		}
	}
}
