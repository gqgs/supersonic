package widgets

import (
	"context"
	"image"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/test"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"github.com/supersonic-app/supersonic/ui/util"
)

type sizedRecoveryFetcher struct {
	recoveryFetcher
	sizes      []int
	cacheSizes []int
}

func (f *sizedRecoveryFetcher) GetCoverArtSizedFromCache(_ string, size int) (image.Image, bool) {
	f.cacheSizes = append(f.cacheSizes, size)
	return nil, false
}
func (f *sizedRecoveryFetcher) GetCoverArtSizedAsync(id string, size int, cb func(image.Image, error)) context.CancelFunc {
	f.sizes = append(f.sizes, size)
	return f.GetCoverThumbnailAsync(id, cb)
}
func TestSizedThumbnailReplacesObsoleteQueuedResult(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 10)
	fyne.SetCurrentApp(&searchTestApp{App: app, driver: &searchTestDriver{Driver: app.Driver(), queue: queue}})
	f := &sizedRecoveryFetcher{}
	var rendered image.Image
	size := 128
	loader := util.NewThumbnailLoader(f, func(img image.Image) { rendered = img })
	loader.RequestSize = func() int { return size }
	loader.Load("same")
	old := image.NewRGBA(image.Rect(0, 0, 128, 128))
	f.callbacks[0](old, nil) // queued before cancellation
	size = 600
	if !loader.NeedsReload() {
		t.Fatal("larger display not invalidated")
	}
	loader.Load("same")
	(<-queue)()
	if rendered != nil {
		t.Fatal("obsolete small artwork applied")
	}
	large := image.NewRGBA(image.Rect(0, 0, 600, 600))
	f.callbacks[1](large, nil)
	(<-queue)()
	if rendered != large || loader.NeedsReload() {
		t.Fatal("new size not retained")
	}
	if len(f.sizes) != 2 || f.sizes[0] != 128 || f.sizes[1] != 600 || f.cacheSizes[1] != 600 {
		t.Fatalf("incorrect size requests: %v %v", f.sizes, f.cacheSizes)
	}
	loader.Cancel()
}

func TestRowsReloadOnlyForNewSizeBucket(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	model := &util.TrackListModel{Item: &mediaprovider.Track{ID: "track", CoverArtID: "cover"}}
	queue := NewPlayQueueList(nil, false)
	queueRow := NewPlayQueueListRow(queue, nil, layout.NewSpacer())
	tracklist := NewTracklist(nil, nil, false)
	trackRow := NewExpandedTracklistRow(tracklist, nil, layout.NewSpacer())
	for _, tc := range []struct {
		name   string
		loader *util.ThumbnailLoader
		update func()
	}{
		{"queue", &queueRow.imageLoader, func() { queueRow.Update(model, 1) }},
		{"track", &trackRow.imageLoader, func() { trackRow.Update(model, 1, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &sizedRecoveryFetcher{}
			size := 128
			*tc.loader = util.NewThumbnailLoader(f, func(image.Image) {})
			tc.loader.RequestSize = func() int { return size }
			tc.update()
			tc.update()
			if len(f.sizes) != 1 {
				t.Fatalf("same bucket duplicated work: %v", f.sizes)
			}
			size = 256
			tc.update()
			tc.update()
			if len(f.sizes) != 2 || f.sizes[1] != 256 {
				t.Fatalf("same track did not upgrade once: %v", f.sizes)
			}
			tc.loader.Cancel()
		})
	}
}
