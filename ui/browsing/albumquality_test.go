package browsing

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

func TestFormatAlbumAudioQuality(t *testing.T) {
	for _, tt := range []struct {
		name   string
		values []string
		want   string
	}{
		{"unknown", nil, ""},
		{"unsafe", []string{"<b>Hi-Res</b>", "stereo", "24-bit"}, ""},
		{"lossless", []string{"lossless", "lossless"}, "Lossless"},
		{"hires", []string{"lossless", "hi-res-lossless"}, "Hi-Res Lossless"},
		{"atmos", []string{"lossless", "dolby-atmos"}, "Dolby Atmos · Lossless"},
		{"all", []string{"lossless", "dolby-audio", "hi-res-lossless", "dolby-atmos"}, "Dolby Atmos · Hi-Res Lossless"},
		{"audio", []string{"dolby-audio"}, "Dolby Audio"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatAlbumAudioQuality(tt.values); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func qualityTestPage() *AlbumPage {
	p := &AlbumPage{albumPageState: albumPageState{albumID: "album"}, loadGeneration: 1}
	p.header = NewAlbumPageHeader(p)
	p.header.albumID = "album"
	return p
}

func TestAlbumQualityVisibilityAndReuse(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	p := qualityTestPage()
	if p.header.qualityLabel.Importance != widget.MediumImportance || !p.header.qualityLabel.TextStyle.Bold {
		t.Fatal("catalog availability must use readable foreground text with bold weight")
	}
	p.applyAudioQuality(1, 0, "album", []string{"dolby-atmos", "lossless"})
	if p.header.qualityLabel.Text != "Available: Dolby Atmos · Lossless" || !p.header.qualityLabel.Visible() {
		t.Fatal("expected visible catalog availability")
	}
	p.header.Compact = true
	p.header.Refresh()
	if p.header.qualityLabel.Visible() {
		t.Fatal("compact header must hide quality")
	}
	p.header.Compact = false
	p.header.Refresh()
	if !p.header.qualityLabel.Visible() {
		t.Fatal("expanded header lost quality")
	}
	p.applyAudioQuality(1, 0, "album", nil)
	if p.header.qualityLabel.Text != "" || p.header.qualityLabel.Visible() {
		t.Fatal("empty availability retained a badge")
	}
	p.applyAudioQuality(1, 0, "album", []string{"lossless"})
	p.header.Clear()
	if p.header.qualityLabel.Text != "" || p.header.qualityLabel.Visible() {
		t.Fatal("clear retained quality")
	}
	p.applyAudioQuality(1, 0, "album", []string{"lossless"})
	if p.header.qualityLabel.Text != "" {
		t.Fatal("cleared header accepted old response")
	}
}

func TestAlbumQualityRejectsStaleResponses(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	for _, tt := range []struct {
		name       string
		invalidate func(*AlbumPage)
	}{
		{"navigation", func(p *AlbumPage) { p.disposed = true }},
		{"reload", func(p *AlbumPage) { p.loadGeneration++ }},
		{"header update", func(p *AlbumPage) { p.header.clearAudioQuality() }},
		{"pooled header reused", func(p *AlbumPage) { p.header.page = &AlbumPage{} }},
		{"different album", func(p *AlbumPage) { p.header.albumID = "other" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := qualityTestPage()
			tt.invalidate(p)
			p.applyAudioQuality(1, 0, "album", []string{"lossless"})
			if p.header.qualityLabel.Text != "" || p.header.qualityLabel.Visible() {
				t.Fatal("stale response changed header")
			}
		})
	}
}

type delayedAlbumQualityProvider struct {
	mediaprovider.MediaProvider
	release chan struct{}
}

func (p *delayedAlbumQualityProvider) GetAlbumAudioQuality(string) ([]string, error) {
	<-p.release
	return []string{"hi-res-lossless"}, nil
}

func TestAlbumQualityQueuedResponseAfterReload(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 10)
	fyne.SetCurrentApp(&clearSearchApp{App: app, driver: &clearSearchDriver{Driver: app.Driver(), queue: queue}})
	provider := &delayedAlbumQualityProvider{release: make(chan struct{})}
	p := qualityTestPage()
	p.mp = provider
	p.loadAudioQuality(1)
	close(provider.release)
	select {
	case apply := <-queue:
		p.loadGeneration++
		apply()
	case <-time.After(5 * time.Second):
		t.Fatal("quality response did not arrive")
	}
	if p.header.qualityLabel.Text != "" {
		t.Fatal("queued response survived reload")
	}
}
