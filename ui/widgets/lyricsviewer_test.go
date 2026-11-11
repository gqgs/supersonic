package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestLyricsFollowPlaybackJumpsAndSeeks(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	v := NewLyricsViewer(nil)
	v.SetLyrics(&mediaprovider.Lyrics{Synced: true, Lines: []mediaprovider.LyricLine{
		{Text: "First", Start: 2}, {Text: "Second", Start: 4}, {Text: "Together", Start: 4}, {Text: "Last", Start: 9},
	}})
	for _, tc := range []struct {
		pos  float64
		want int
	}{{0, 0}, {2, 1}, {7, 3}, {20, 4}, {21, 4}, {1, 0}, {4, 3}} {
		v.UpdatePlayPos(tc.pos)
		if v.currentLine != tc.want {
			t.Fatalf("at %v: got %d want %d", tc.pos, v.currentLine, tc.want)
		}
	}
	v.OnSeeked(9)
	if v.currentLine != 4 {
		t.Fatal("seek did not update")
	}
	v.SetLyrics(&mediaprovider.Lyrics{Lines: []mediaprovider.LyricLine{{Text: "Plain"}}})
	v.UpdatePlayPos(100)
	if v.currentLine != 0 || v.status.Text != "Unsynced lyrics" {
		t.Fatal("plain lyrics pretended to sync")
	}
	v.SetLyrics(nil)
	v.UpdatePlayPos(100)
}
func TestRejectMalformedLyricTiming(t *testing.T) {
	for _, starts := range [][]float64{{2, 1}, {-1}, {math.NaN()}, {math.Inf(1)}} {
		l := &mediaprovider.Lyrics{Synced: true}
		for _, s := range starts {
			l.Lines = append(l.Lines, mediaprovider.LyricLine{Start: s})
		}
		if validLyricTiming(l) {
			t.Fatal("accepted malformed timing", starts)
		}
	}
}

func TestLyricsPresentationPreview(t *testing.T) {
	dir := os.Getenv("SUPERSONIC_LYRICS_PREVIEW_DIR")
	if dir == "" {
		t.Skip("optional headless presentation capture")
	}
	app := test.NewApp()
	defer app.Quit()
	v := NewLyricsViewer(nil)
	v.SetLyrics(&mediaprovider.Lyrics{Synced: true, Lines: []mediaprovider.LyricLine{
		{Text: "A little light across the water", Start: 1}, {Text: "The quiet space between the waves", Start: 4}, {Text: "We follow where the current takes us", Start: 8}, {Text: "And let the morning find its way", Start: 12},
	}})
	window := test.NewWindow(v)
	defer window.Close()
	window.Resize(fyne.NewSize(660, 600))
	window.Show()
	v.OnSeeked(5)
	f, err := os.Create(filepath.Join(dir, "lyrics-synced.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, window.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
	v.SetLyrics(&mediaprovider.Lyrics{Lines: []mediaprovider.LyricLine{{Text: "Plain lyrics remain readable"}, {Text: "Without pretending to follow the recording"}}})
	f2, err := os.Create(filepath.Join(dir, "lyrics-plain.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()
	if err = png.Encode(f2, window.Canvas().Capture()); err != nil {
		t.Fatal(err)
	}
}
