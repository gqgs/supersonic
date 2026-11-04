package util

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

type artworkCanvas struct {
	fyne.Canvas
	backing float32
}

func (c artworkCanvas) Scale() float32 { return 1 }
func (c artworkCanvas) PixelCoordinateForPosition(p fyne.Position) (int, int) {
	return int(p.X * c.backing), int(p.Y * c.backing)
}

type artworkDriver struct {
	fyne.Driver
	c fyne.Canvas
}

func (d artworkDriver) CanvasForObject(fyne.CanvasObject) fyne.Canvas { return d.c }

type artworkApp struct {
	fyne.App
	d fyne.Driver
}

func (a artworkApp) Driver() fyne.Driver { return a.d }
func TestArtworkSizeIncludesRetinaBackingScale(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	object := canvas.NewRectangle(color.White)
	object.SetMinSize(fyne.NewSquareSize(200))
	for _, tc := range []struct {
		backing float32
		want    int
	}{{1, 256}, {2, 400}, {3, 600}} {
		fyne.SetCurrentApp(artworkApp{App: app, d: artworkDriver{Driver: app.Driver(), c: artworkCanvas{backing: tc.backing}}})
		if got := ArtworkSize(object); got != tc.want {
			t.Fatalf("backing %v: got %d want %d", tc.backing, got, tc.want)
		}
	}
	fyne.SetCurrentApp(app)
	object.Resize(fyne.NewSquareSize(565))
	fyne.SetCurrentApp(artworkApp{App: app, d: artworkDriver{Driver: app.Driver(), c: artworkCanvas{backing: 2}}})
	if got := ArtworkSize(object); got != 1200 {
		t.Fatalf("565pt Retina cover requested %d", got)
	}
}
