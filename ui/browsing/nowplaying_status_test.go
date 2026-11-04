package browsing

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/test"
	"testing"
)

func TestNowPlayingOutputStatusWrapsWithoutWideningWindow(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	label := newNowPlayingStatusLabel()
	label.SetText("Playing (12/234) 03:14/05:27 · Total time: 19:32:15 | Source: EAC3, 768 kbps • Output: EAC3 passthrough 192 kHz")
	footer := container.NewVBox(layout.NewSpacer(), label)
	win := app.NewWindow("Now Playing status")
	defer win.Close()
	win.SetContent(footer)
	var narrowHeight float32
	for _, width := range []float32{320, 640, 1024} {
		win.Resize(fyne.NewSize(width, 240))
		footer.Resize(fyne.NewSize(width, 240))
		label.Refresh()
		if footer.MinSize().Width > width || label.Size().Width > width {
			t.Fatalf("status expands %g-pixel layout: min=%v label=%v", width, footer.MinSize(), label.Size())
		}
		if width == 320 {
			narrowHeight = label.MinSize().Height
		}
	}
	if narrowHeight <= label.MinSize().Height {
		t.Fatalf("text did not wrap: narrow height=%g wide=%g", narrowHeight, label.MinSize().Height)
	}
}
