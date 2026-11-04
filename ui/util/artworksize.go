package util

import (
	"fyne.io/fyne/v2"
	"github.com/supersonic-app/supersonic/backend"
)

// ArtworkSize uses physical pixels, including the canvas' HiDPI/UI scale.
// Call on the UI thread. Before first layout, use the object's minimum size.
func ArtworkSize(object fyne.CanvasObject) int {
	size := object.Size()
	if size.Width <= 0 || size.Height <= 0 {
		size = object.MinSize()
	}
	pixels := max(size.Width, size.Height)
	if app := fyne.CurrentApp(); app != nil && app.Driver() != nil {
		if c := app.Driver().CanvasForObject(object); c != nil {
			// Scale() alone omits the native backing/Retina texture scale.
			x, y := c.PixelCoordinateForPosition(fyne.NewPos(size.Width, size.Height))
			pixels = float32(max(x, y))
		}
	}
	return backend.CoverArtSize(pixels, 1)
}
