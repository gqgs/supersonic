package backend

import (
	"context"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"testing"
)

func TestPreferTimedLyricsKeepsPlainOnMiss(t *testing.T) {
	plain := &mediaprovider.Lyrics{Lines: []mediaprovider.LyricLine{{Text: "Server text"}}}
	other := &mediaprovider.Lyrics{Lines: []mediaprovider.LyricLine{{Text: "Other plain text"}}}
	timed := &mediaprovider.Lyrics{Synced: true, Lines: []mediaprovider.LyricLine{{Text: "Timed", Start: 3}}}
	for _, tc := range []struct{ primary, fallback, want *mediaprovider.Lyrics }{{plain, nil, plain}, {plain, other, plain}, {plain, timed, timed}, {nil, other, other}, {&mediaprovider.Lyrics{}, other, other}, {plain, &mediaprovider.Lyrics{Synced: true}, plain}} {
		if preferTimedLyrics(tc.primary, tc.fallback) != tc.want {
			t.Fatal("wrong source selection")
		}
	}
}
func TestDisabledLyricsFallbackRemainsNil(t *testing.T) {
	var disabled *LrcLibFetcher
	if NewLyricsManager(nil, disabled).lrclib != nil {
		t.Fatal("nil concrete fetcher became a non-nil interface")
	}
}

type lyricTestProvider struct {
	mediaprovider.MediaProvider
	lyrics *mediaprovider.Lyrics
}

func (p lyricTestProvider) GetLyrics(*mediaprovider.Track) (*mediaprovider.Lyrics, error) {
	return p.lyrics, nil
}

type lyricTestFallback struct {
	calls  int
	lyrics *mediaprovider.Lyrics
}

func (f *lyricTestFallback) FetchLrcLibLyrics(string, string, string, int) (*mediaprovider.Lyrics, error) {
	f.calls++
	return f.lyrics, nil
}
func TestPlainLyricsCanUpgradeButTimedSkipsFallback(t *testing.T) {
	plain := &mediaprovider.Lyrics{Lines: []mediaprovider.LyricLine{{Text: "Plain"}}}
	timed := &mediaprovider.Lyrics{Synced: true, Lines: []mediaprovider.LyricLine{{Text: "Timed", Start: 2}}}
	for _, tc := range []struct {
		source *mediaprovider.Lyrics
		calls  int
	}{{plain, 1}, {timed, 0}} {
		fallback := &lyricTestFallback{lyrics: timed}
		lm := &LyricsManager{sm: &ServerManager{Server: lyricTestProvider{lyrics: tc.source}}, lrclib: fallback}
		lm.fetchLyrics(context.Background(), &mediaprovider.Track{}, func(_ string, got *mediaprovider.Lyrics) {
			if got != timed {
				t.Fatal("did not prefer timed lyrics")
			}
		})
		if fallback.calls != tc.calls {
			t.Fatal("unexpected fallback request")
		}
	}
}
func TestCanceledLyricsDoNotQueryFallbackOrDeliver(t *testing.T) {
	fallback := &lyricTestFallback{}
	lm := &LyricsManager{sm: &ServerManager{Server: lyricTestProvider{}}, lrclib: fallback}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lm.fetchLyrics(ctx, &mediaprovider.Track{}, func(string, *mediaprovider.Lyrics) { t.Fatal("delivered canceled lyrics") })
	if fallback.calls != 0 {
		t.Fatal("queried after cancellation")
	}
}
