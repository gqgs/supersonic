package browsing

import (
	"fyne.io/fyne/v2/canvas"
	"github.com/supersonic-app/supersonic/backend"
	"image"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/supersonic-app/supersonic/ui/widgets"
)

func TestNowPlayingRejectsQueuedObsoleteArtwork(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 10)
	fyne.SetCurrentApp(&clearSearchApp{App: app, driver: &clearSearchDriver{Driver: app.Driver(), queue: queue}})
	p := &NowPlayingPage{card: widgets.NewLargeNowPlayingCard(), artworkActive: true, coverRequest: 1}
	old := image.NewRGBA(image.Rect(0, 0, 128, 128))
	p.onImageLoaded(old, nil, 1, "old", false)
	p.coverRequest = 2
	(<-queue)()
	cover := p.card.Content.(*widgets.ImagePlaceholder)
	if cover.Image() != nil || p.curCoverArt != "" {
		t.Fatal("stale queued cover rendered")
	}
	newer := image.NewRGBA(image.Rect(0, 0, 600, 600))
	p.onImageLoaded(newer, nil, 2, "new", false)
	(<-queue)()
	if cover.Image() != newer || p.curCoverArt != "new" {
		t.Fatal("current artwork lost")
	}
	p.onImageLoaded(old, nil, 2, "new", false)
	p.artworkActive = false // navigating away also invalidates already queued work
	(<-queue)()
	if cover.Image() != newer {
		t.Fatal("hidden page accepted queued artwork")
	}
}

func TestNowPlayingEmptyCoverClearsBackground(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	old := image.NewRGBA(image.Rect(0, 0, 16, 16))
	p := &NowPlayingPage{
		card: widgets.NewLargeNowPlayingCard(), artworkActive: true,
		coverRequestID: "old", coverRequestSize: 600,
		backgroundImgA:      canvas.NewImageFromImage(old),
		backgroundImgB:      canvas.NewImageFromImage(old),
		backgroundGradient:  canvas.NewLinearGradient(color.White, color.Transparent, 0),
		nowPlayingPageState: nowPlayingPageState{im: &backend.ImageManager{}},
	}
	p.card.SetCoverImage(old)
	p.loadCoverForDisplay()
	if !p.backgroundImgA.Hidden || !p.backgroundImgB.Hidden || p.backgroundImgA.Image != nil || p.backgroundImgB.Image != nil || p.backgroundGradient.StartColor != color.Transparent {
		t.Fatal("previous cover remained in background after empty artwork")
	}
	if p.card.Content.(*widgets.ImagePlaceholder).Image() != nil {
		t.Fatal("previous cover remained in card")
	}
	request := p.coverRequest
	p.loadCoverForDisplay()
	if p.coverRequest != request {
		t.Fatal("empty cover repeated work")
	}
}
