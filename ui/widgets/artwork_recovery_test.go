package widgets

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	myTheme "github.com/supersonic-app/supersonic/ui/theme"
	"github.com/supersonic-app/supersonic/ui/util"
)

type recoveryFetcher struct {
	ids        []string
	callbacks  []func(image.Image, error)
	cancelled  []bool
	generation uint64
}

func (f *recoveryFetcher) ImageCacheGeneration() uint64                          { return f.generation }
func (f *recoveryFetcher) GetCoverThumbnailFromCache(string) (image.Image, bool) { return nil, false }
func (f *recoveryFetcher) GetCoverThumbnailAsync(id string, cb func(image.Image, error)) context.CancelFunc {
	index := len(f.ids)
	f.ids = append(f.ids, id)
	f.callbacks = append(f.callbacks, cb)
	f.cancelled = append(f.cancelled, false)
	return func() { f.cancelled[index] = true }
}

func TestGroupedArtworkRecoveryAndRendering(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 100)
	fyne.SetCurrentApp(&searchTestApp{App: app, driver: &searchTestDriver{Driver: app.Driver(), queue: queue}})
	fetch := &recoveryFetcher{}
	card := NewGridViewItem(myTheme.AlbumIcon)
	card.ImgLoader = util.NewThumbnailLoader(fetch, card.Cover.SetImage)
	card.ImgLoader.OnBeforeLoad = func() { card.Cover.SetImage(nil) }
	group := &GroupedReleases{}
	for i := range group.sections {
		group.sections[i].container = container.NewVBox()
	}
	group.sections[0].container.Add(card)
	model := GridViewItemModel{ID: "album", Name: "Album", CoverArtID: "cover1"}
	group.doUpdateItemCard(card, &model)
	fetch.callbacks[0](nil, errors.New("temporary outage"))
	(<-queue)()
	group.doUpdateItemCard(card, &model)
	if len(fetch.ids) != 1 {
		t.Fatal("ordinary refresh hot-retried failed image")
	}
	group.ReloadImages()
	if len(fetch.ids) != 2 {
		t.Fatal("explicit reload did not retry")
	}
	artwork := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			artwork.Set(x, y, color.RGBA{0x33, 0x66, 0x99, 255})
		}
	}
	fetch.callbacks[1](artwork, nil)
	(<-queue)()
	if !card.Cover.Im.HaveImage() || card.Cover.Im.imageDisp.Hidden || !card.Cover.Im.iconImage.Hidden {
		t.Fatal("successful retry did not replace placeholder")
	}
	win := app.NewWindow("recovered artwork")
	defer win.Close()
	win.SetContent(card)
	win.Resize(fyne.NewSize(240, 280))
	rendered := win.Canvas().Capture()
	found := false
	for y := 0; y < rendered.Bounds().Dy(); y++ {
		for x := 0; x < rendered.Bounds().Dx(); x++ {
			r, g, b, _ := rendered.At(x, y).RGBA()
			if r>>8 == 0x33 && g>>8 == 0x66 && b>>8 == 0x99 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("recovered artwork not present in rendered canvas")
	}
	model.CoverArtID = "cover2"
	group.doUpdateItemCard(card, &model)
	if len(fetch.ids) != 3 || fetch.ids[2] != "cover2" {
		t.Fatal("changed cover ID ignored")
	}
	// An older failure already queued on the UI thread must neither clear the
	// new image nor cancel its pending request.
	fetch.callbacks[1](nil, errors.New("stale failure"))
	(<-queue)()
	if fetch.cancelled[2] {
		t.Fatal("stale callback cancelled current image request")
	}
	fetch.callbacks[2](artwork, nil)
	(<-queue)()
	if !card.Cover.Im.HaveImage() {
		t.Fatal("new cover missing")
	}
	fetch.generation++ // ImageManager.ClearInMemoryCache performs this invalidation.
	group.doUpdateItemCard(card, &model)
	if len(fetch.ids) != 4 {
		t.Fatal("cache clearing did not invalidate visible card")
	}
	fetch.callbacks[3](artwork, nil)
	(<-queue)()
	if !card.Cover.Im.HaveImage() {
		t.Fatal("cache-clear refresh did not recover")
	}
}
