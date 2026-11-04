package widgets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"image"
	"image/color"
	"log"
	"strings"
	"testing"
	"time"

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

func TestGroupedArtworkColdScrollIsViewportBounded(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 1000)
	fyne.SetCurrentApp(&searchTestApp{App: app, driver: &searchTestDriver{Driver: app.Driver(), queue: queue}})
	fetch := &recoveryFetcher{}
	model := GroupedReleasesModel{}
	for n := 0; n < 120; n++ {
		id := fmt.Sprintf("album-%03d", n)
		model.Albums = append(model.Albums, GridViewItemModel{ID: id, Name: id, CoverArtID: id})
	}
	group := NewGroupedReleases(model, fetch)
	win := app.NewWindow("cold scroll")
	defer win.Close()
	win.SetContent(group)
	win.Resize(fyne.NewSize(650, 500))
	group.Refresh()
	cold := len(fetch.ids)
	if cold == 0 || cold > 20 {
		t.Fatalf("cold viewport requested %d of 120 images", cold)
	}
	for n := 0; n < 5; n++ {
		group.Refresh()
	}
	if len(fetch.ids) != cold {
		t.Fatal("paint refresh duplicated outstanding requests")
	}
	group.ScrollToOffset(2500)
	scrolled := len(fetch.ids) - cold
	if scrolled == 0 || scrolled > 20 {
		t.Fatalf("scroll requested %d images", scrolled)
	}
	cancelled := 0
	for _, v := range fetch.cancelled[:cold] {
		if v {
			cancelled++
		}
	}
	if cancelled != cold {
		t.Fatalf("only %d of %d offscreen requests cancelled", cancelled, cold)
	}
	artwork := image.NewRGBA(image.Rect(0, 0, 400, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			artwork.Set(x, y, color.RGBA{0x33, 0x66, 0x99, 255})
		}
	}
	for n := cold; n < len(fetch.callbacks); n++ {
		fetch.callbacks[n](artwork, nil)
	}
	for len(queue) > 0 {
		(<-queue)()
	}
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
		t.Fatal("scrolled artwork did not render")
	}
	beforeWheel := len(fetch.ids)
	group.container.Scrolled(&fyne.ScrollEvent{Scrolled: fyne.Delta{DY: 2500}})
	if len(fetch.ids) <= beforeWheel {
		t.Fatal("wheel scrolling did not load new viewport")
	}
	group.sections[0].titleRow.Tapped(nil)
	if len(group.visibleArtwork) != 0 {
		t.Fatal("collapsed section retained image work")
	}
	group.sections[0].titleRow.Tapped(nil)
	if len(group.visibleArtwork) == 0 {
		t.Fatal("expanded section did not reload visible artwork")
	}
	group.CancelImages()
	afterCancel := len(fetch.ids)
	test.WidgetRenderer(group).Refresh()
	if len(fetch.ids) != afterCancel {
		t.Fatal("renderer restarted artwork after page disposal")
	}
	for _, v := range fetch.cancelled {
		if !v {
			t.Fatal("leaving page retained image work")
		}
	}
	t.Logf("120 releases: cold viewport=%d requests, next viewport=%d, offscreen cancellations=%d", cold, scrolled, cancelled)
}

func TestExpectedArtworkAbsenceIsQuietButFailuresRemainVisible(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 10)
	fyne.SetCurrentApp(&searchTestApp{App: app, driver: &searchTestDriver{Driver: app.Driver(), queue: queue}})
	fetch := &recoveryFetcher{}
	var rendered image.Image
	loader := util.NewThumbnailLoader(fetch, func(img image.Image) { rendered = img })
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)
	for _, err := range []error{mediaprovider.ErrArtworkUnavailable, fmt.Errorf("cooldown: %w", mediaprovider.ErrArtworkPending)} {
		loader.Load("artist-image:1")
		fetch.callbacks[len(fetch.callbacks)-1](nil, err)
		(<-queue)()
	}
	if strings.Contains(output.String(), "Error loading cover image") {
		t.Fatalf("expected state logged as error: %s", output.String())
	}
	if rendered != nil {
		t.Fatal("missing artwork fabricated an image")
	}
	loader.Load("artist-image:1")
	fetch.callbacks[len(fetch.callbacks)-1](nil, errors.New("Subsonic artwork error 40: authentication failed"))
	(<-queue)()
	if !strings.Contains(output.String(), "authentication failed") {
		t.Fatal("real failure hidden")
	}
}

func TestVisibleArtworkRetriesAfterCooldownAndCancels(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 100)
	fyne.SetCurrentApp(&searchTestApp{App: app, driver: &searchTestDriver{Driver: app.Driver(), queue: queue}})
	fetch := &recoveryFetcher{}
	card := NewGridViewItem(myTheme.AlbumIcon)
	card.ImgLoader = util.NewThumbnailLoader(fetch, card.Cover.SetImage)
	drain := func() {
		t.Helper()
		select {
		case fn := <-queue:
			fn()
		case <-time.After(2 * time.Second):
			t.Fatal("callback did not arrive")
		}
	}
	pending := func() error {
		return &mediaprovider.ArtworkRetryError{Err: mediaprovider.ErrArtworkPending, RetryAt: time.Now().Add(time.Second)}
	}
	card.ImgLoader.Load("artist-release:1")
	fetch.callbacks[0](nil, pending())
	drain()
	if len(fetch.ids) != 1 {
		t.Fatal("retry before cooldown")
	}
	drain()
	if len(fetch.ids) != 2 {
		t.Fatal("visible card did not retry")
	}
	art := image.NewRGBA(image.Rect(0, 0, 10, 10))
	fetch.callbacks[1](art, nil)
	drain()
	if !card.Cover.Im.HaveImage() {
		t.Fatal("retry did not replace placeholder")
	}
	card.ImgLoader.Load("artist-release:2")
	fetch.callbacks[2](nil, pending())
	drain()
	// Let the timer queue its callback, then hide before executing it.
	time.Sleep(1100 * time.Millisecond)
	card.Hide()
	drain()
	if len(fetch.ids) != 3 {
		t.Fatal("hidden card retried")
	}
	card.Show()
	card.ImgLoader.Load("artist-release:3")
	for n := 0; n < 4; n++ {
		fetch.callbacks[len(fetch.callbacks)-1](nil, pending())
		drain()
		if n < 3 {
			drain()
		}
	}
	select {
	case <-queue:
		t.Fatal("unbounded retries")
	case <-time.After(1100 * time.Millisecond):
	}
	if len(fetch.ids) != 7 {
		t.Fatalf("expected 3 deferred retries, got %d total requests", len(fetch.ids))
	}
	card.ImgLoader.CanRetry = func() bool { return false }
	card.ImgLoader.Load("artist-release:offscreen")
	fetch.callbacks[7](nil, pending())
	drain()
	drain()
	if len(fetch.ids) != 8 || !card.ImgLoader.NeedsReload() {
		t.Fatal("offscreen retry must defer until card returns")
	}
	card.ImgLoader.Cancel()
}
