package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	fynelyrics "github.com/supersonic-app/fyne-lyrics"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"github.com/supersonic-app/supersonic/ui/theme"
	"image/color"
	"math"
	"sort"
)

type LyricsViewer struct {
	widget.BaseWidget

	noLyricsMsg fyne.CanvasObject
	viewer      *fynelyrics.LyricsViewer
	lyrics      *mediaprovider.Lyrics
	currentLine int
	status      *widget.Label
	themed      *container.ThemeOverride
	lastPlayPos float64

	// keeps track if UpdatePlayPos has been called yet
	// for the current lyrics
	firstUpdate bool

	onSeekToLine func(int)

	container *fyne.Container
	isEmpty   bool
}

func NewLyricsViewer(onSeekToLine func(int)) *LyricsViewer {
	l := &LyricsViewer{
		noLyricsMsg: container.NewCenter(NewInfoMessage(
			lang.L("Lyrics not available"), "")),
		isEmpty:      true,
		onSeekToLine: onSeekToLine,
	}
	l.ExtendBaseWidget(l)
	l.container = container.NewStack(l.noLyricsMsg)
	return l
}

func (l *LyricsViewer) DisableTapToSeek() {
	if l.viewer != nil {
		l.viewer.OnLyricTapped = nil
	}
}

func (l *LyricsViewer) EnableTapToSeek() {
	if l.viewer != nil {
		l.viewer.OnLyricTapped = l.onSeekToLine
	}
}

func (l *LyricsViewer) SetLyrics(lyrics *mediaprovider.Lyrics) {
	l.lyrics = lyrics
	l.currentLine = 0
	l.firstUpdate = true
	if lyrics == nil || len(lyrics.Lines) == 0 {
		if !l.isEmpty {
			l.container.Objects[0] = l.noLyricsMsg
			l.isEmpty = true
			l.Refresh()
		}
		return
	}

	if l.viewer == nil {
		l.viewer = fynelyrics.NewLyricsViewer()
		l.viewer.ActiveLyricPosition = fynelyrics.ActiveLyricPositionUpperMiddle
		l.viewer.InactiveLyricColorName = theme.ColorNameInactiveLyric
		l.viewer.OnLyricTapped = l.onSeekToLine
	}
	lines := make([]string, len(lyrics.Lines))
	for i, line := range lyrics.Lines {
		lines[i] = line.Text
	}
	// Reject malformed timing rather than showing a false moving highlight.
	synced := validLyricTiming(lyrics)
	l.viewer.ActiveLyricColorName = fynetheme.ColorNameForeground
	if synced {
		l.viewer.ActiveLyricColorName = fynetheme.ColorNamePrimary
	}
	l.viewer.SetLyrics(lines, synced)
	if l.status == nil {
		l.status = widget.NewLabel("")
		l.status.Importance = widget.LowImportance
	}
	if synced {
		l.status.SetText(lang.L("Synced lyrics"))
	} else {
		l.status.SetText(lang.L("Unsynced lyrics"))
	}
	if l.themed == nil {
		l.themed = container.NewThemeOverride(l.viewer, lyricTheme{})
	}
	l.themed.Refresh()
	if l.isEmpty {
		l.container.Objects[0] = container.New(layout.NewCustomPaddedLayout(12, 12, 16, 16),
			container.NewBorder(l.status, nil, nil, nil,
				l.themed))
		l.isEmpty = false
		l.Refresh()
	}
}

// validLyricTiming never invents timing for plain or malformed lyrics.
func validLyricTiming(lyrics *mediaprovider.Lyrics) bool {
	if lyrics == nil || !lyrics.Synced || len(lyrics.Lines) == 0 {
		return false
	}
	previous := -1.0
	for _, line := range lyrics.Lines {
		if math.IsNaN(line.Start) || math.IsInf(line.Start, 0) || line.Start < 0 || line.Start < previous {
			return false
		}
		previous = line.Start
	}
	return true
}

func lyricLineAt(lines []mediaprovider.LyricLine, seconds float64) int {
	// One-indexed; zero is the intro. Equal timestamps choose the last such line.
	return sort.Search(len(lines), func(i int) bool { return lines[i].Start > seconds })
}

func (l *LyricsViewer) UpdatePlayPos(seconds float64) {
	if !validLyricTiming(l.lyrics) || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	current := lyricLineAt(l.lyrics.Lines, seconds)
	if !l.firstUpdate && seconds >= l.lastPlayPos && current == l.currentLine+1 {
		l.viewer.NextLine()
	} else if l.firstUpdate || current != l.currentLine {
		l.viewer.SetCurrentLine(current)
	}
	l.currentLine, l.lastPlayPos, l.firstUpdate = current, seconds, false
}

func (l *LyricsViewer) OnSeeked(seconds float64) {
	l.firstUpdate = true
	l.UpdatePlayPos(seconds)
}

// Only lyrics use regular-weight, larger type; the rest of the app is unchanged.
type lyricTheme struct{}

func (lyricTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	return fyne.CurrentApp().Settings().Theme().Color(n, v)
}
func (lyricTheme) Font(s fyne.TextStyle) fyne.Resource {
	s.Bold = false
	return fyne.CurrentApp().Settings().Theme().Font(s)
}
func (lyricTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return fyne.CurrentApp().Settings().Theme().Icon(n)
}
func (lyricTheme) Size(n fyne.ThemeSizeName) float32 {
	if n == fynetheme.SizeNameSubHeadingText {
		return fyne.CurrentApp().Settings().Theme().Size(fynetheme.SizeNameText) + 6
	}
	return fyne.CurrentApp().Settings().Theme().Size(n)
}

func (l *LyricsViewer) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.container)
}
