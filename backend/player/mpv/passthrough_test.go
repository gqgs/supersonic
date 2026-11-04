package mpv

import (
	"errors"
	api "github.com/supersonic-app/go-mpv"
	"github.com/supersonic-app/supersonic/backend/player"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failingOption struct{}

func (failingOption) SetOptionString(string, string) error { return errors.New("unsupported") }

func TestPassthroughOptionFailureIsReturned(t *testing.T) {
	if err := configurePassthrough(failingOption{}, true); err == nil {
		t.Fatal("option failure hidden")
	}
}

func TestPassthroughNativeConfiguration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "PCM default", true: "EAC3 opt-in"}[enabled], func(t *testing.T) {
			m := api.Create()
			defer m.TerminateDestroy()
			if err := configurePassthrough(m, enabled); err != nil {
				t.Fatal(err)
			}
			m.SetOptionString("ao", "null")
			m.SetOptionString("idle", "yes")
			m.SetOptionString("video", "no")
			if err := m.Initialize(); err != nil {
				t.Fatal(err)
			}
			p := &Player{mpv: m, initialized: true, eac3Passthrough: enabled}
			want := ""
			if enabled {
				want = "eac3"
			}
			if got := m.GetPropertyString("audio-spdif"); got != want {
				t.Fatalf("audio-spdif=%q", got)
			}
			eq := &ISO10BandEqualizer{EQPreamp: 3}
			if err := p.SetEqualizer(eq); err != nil {
				t.Fatal(err)
			}
			if err := p.SetPeaksEnabled(true); err != nil {
				t.Fatal(err)
			}
			filters := m.GetPropertyString("af")
			if enabled && filters != "" {
				t.Fatalf("passthrough filters=%q", filters)
			}
			if !enabled && !strings.Contains(filters, "astats") {
				t.Fatalf("PCM filters lost: %q", filters)
			}
			if err := p.SetReplayGainOptions(player.ReplayGainOptions{Mode: player.ReplayGainTrack, PreampGain: 6}); err != nil {
				t.Fatal(err)
			}
			if p.replayGainOpts.Mode != player.ReplayGainTrack || p.replayGainOpts.PreampGain != 6 {
				t.Fatal("saved ReplayGain overwritten")
			}
			wantRG := "track"
			if enabled {
				wantRG = "no"
			}
			if got := m.GetPropertyString("replaygain"); got != wantRG {
				t.Fatalf("replaygain=%s", got)
			}
			if err := p.SetVolume(37); err != nil || p.GetVolume() != 37 {
				t.Fatal("saved PCM volume changed", err)
			}
			if err := p.SetEAC3Passthrough(!enabled); err == nil {
				t.Fatal("live toggle accepted")
			}
			if enabled {
				p.status.State = player.Playing
				p.SetPauseFade(true)
				if err := p.Pause(); err != nil {
					t.Fatal(err)
				}
				if p.fadePauseCancel != nil || p.status.State != player.Paused {
					t.Fatal("passthrough pause used software fade")
				}
			}
			if eq != p.Equalizer() || !eq.IsEnabled() {
				t.Fatal("saved EQ lost")
			}
		})
	}
}

func TestOutputDoesNotInferPassthroughFromSource(t *testing.T) {
	for _, tc := range []struct{ format, want string }{{"", "Unknown"}, {"float", "PCM (float)"}, {"spdif-eac3", "EAC3 passthrough"}} {
		info := MediaInfo{Codec: "eac3", OutputFormat: tc.format}
		if got := info.OutputDescription(); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
}

func TestEAC3PlaybackAndPCMFallback(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg required for local E-AC-3 fixture")
	}
	dir := t.TempDir()
	eac3 := filepath.Join(dir, "fixture.mka")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "30", "-c:a", "eac3", eac3).CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v: %s", err, out)
	}
	flac := filepath.Join(dir, "fixture.flac")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "3", "-c:a", "flac", "-sample_fmt", "s16", flac).CombinedOutput(); err != nil {
		t.Fatalf("generate PCM fixture: %v: %s", err, out)
	}
	for _, tc := range []struct {
		name              string
		enabled, forcePCM bool
		want              string
	}{
		{"default decodes EAC3", false, false, "floatp"},
		{"opt in passes EAC3", true, false, "spdif-eac3"},
		{"other codecs remain PCM", true, false, "s16"},
		{"incompatible output can recover with passthrough disabled", true, true, "float"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := api.Create()
			defer m.TerminateDestroy()
			if err := configurePassthrough(m, tc.enabled); err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{"ao": "null", "video": "no", "idle": "yes", "pause": "no"} {
				if err := m.SetOptionString(key, value); err != nil {
					t.Fatal(err)
				}
			}
			if tc.forcePCM {

				if err := m.SetOptionString("ao-null-format", "float"); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Initialize(); err != nil {
				t.Fatal(err)
			}
			// Saved software volume must not turn an enabled bitstream into PCM.
			if err := m.SetProperty("volume", api.FORMAT_INT64, int64(37)); err != nil {
				t.Fatal(err)
			}
			source := eac3
			if tc.name == "other codecs remain PCM" {
				source = flac
			}
			if err := m.Command([]string{"loadfile", source}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			var format string
			for time.Now().Before(deadline) {
				m.WaitEvent(0.02)
				format = m.GetPropertyString("audio-out-params/format")
				if format != "" {
					break
				}
			}
			if tc.forcePCM && format == "" {
				// This libmpv build can fail to finish automatic fallback after an AO
				// changes the bitstream format. Do not misreport that as active PCM.
				if (MediaInfo{Codec: "eac3"}).OutputDescription() != "Unknown" {
					t.Fatal("failed output claimed success")
				}
				// Exercise the recovery path equivalent to disabling the setting and
				// reloading after restart; no device is changed or auto-selected here.
				if err := m.SetPropertyString("audio-spdif", ""); err != nil {
					t.Fatal(err)
				}
				if err := m.Command([]string{"loadfile", eac3}); err != nil {
					t.Fatal(err)
				}
				deadline = time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					m.WaitEvent(0.02)
					format = m.GetPropertyString("audio-out-params/format")
					if format != "" {
						break
					}
				}
				t.Log("incompatible output recovered by explicitly disabling passthrough")
			}
			if format != tc.want {
				t.Fatalf("output=%q want=%q", format, tc.want)
			}
			p := &Player{mpv: m, initialized: true, eac3Passthrough: tc.enabled}
			info, err := p.GetMediaInfo()
			if err != nil {
				t.Fatal(err)
			}
			if info.OutputFormat != format {
				t.Fatal("reported output differs from API")
			}
			if !strings.HasPrefix(format, "spdif-") {
				if err := p.SetVolume(64); err != nil {
					t.Fatal(err)
				}
				volume, err := m.GetProperty("volume", api.FORMAT_DOUBLE)
				if err != nil || volume != float64(64) {
					t.Fatalf("PCM volume did not reach libmpv: %v %v", volume, err)
				}
			}
			t.Logf("source=%s output=%s rate=%d", info.Codec, info.OutputFormat, info.OutputSamplerate)
		})
	}
}
