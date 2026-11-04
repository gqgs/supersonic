package browsing

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"github.com/supersonic-app/supersonic/ui/widgets"
	"reflect"
	"testing"
	"time"
)

type clearSearchDriver struct {
	fyne.Driver
	queue chan func()
}

func (d *clearSearchDriver) DoFromGoroutine(fn func(), _ bool) { d.queue <- fn }

type clearSearchApp struct {
	fyne.App
	driver fyne.Driver
}

func (a *clearSearchApp) Driver() fyne.Driver { return a.driver }

func TestClearBeforeDebouncedSearchLeavesBrowseStateUntouched(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 20)
	fyne.SetCurrentApp(&clearSearchApp{App: app, driver: &clearSearchDriver{Driver: app.Driver(), queue: queue}})
	page := &GridViewPage[mediaprovider.Artist, mediaprovider.ArtistFilterOptions]{grid: &widgets.GridView{}}
	entry := widgets.NewSearchEntry()
	entry.OnSearched = page.OnSearched
	// The timer's search callback has not run, so no browse snapshot exists.
	entry.SetText("daughter")
	entry.SetText("")
	page.OnSearched("") // repeated clearing must also be harmless
	if page.gridState != nil || page.searchText != "" {
		t.Fatal("clearing unsubmitted text changed browse state")
	}
	// Drain the obsolete debounce callback as the real UI would.
	select {
	case fn := <-queue:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("debounce callback missing")
	}
	if page.gridState != nil || page.searchText != "" {
		t.Fatal("cancelled search reappeared")
	}
	// Also reproduce a timer already queued when the user clears the field.
	entry.SetText("daughter")
	var obsolete func()
	select {
	case obsolete = <-queue:
	case <-time.After(2 * time.Second):
		t.Fatal("queued search missing")
	}
	entry.SetText("")
	obsolete()
	select {
	case fn := <-queue:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("clear timer missing")
	}
	if page.gridState != nil || page.searchText != "" {
		t.Fatal("stale queued search changed browse state")
	}

}

func TestClearCompletedSearchRestoresBrowseState(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	grid := widgets.NewFixedGridView(nil, nil, nil)
	// Supply a lightweight card template without starting the audio/backend app.
	stack := grid.CreateRenderer().Objects()[0].(*fyne.Container)
	wrap := reflect.ValueOf(stack.Objects[0]).Elem().FieldByName("GridWrap").Addr().Interface().(*widget.GridWrap)
	wrap.CreateItem = func() fyne.CanvasObject { return widget.NewLabel("fixture") }
	browse := grid.SaveToState()
	browse.DisableSharing = true
	page := &GridViewPage[mediaprovider.Artist, mediaprovider.ArtistFilterOptions]{grid: grid, gridState: browse, searchText: "daughter", searchGridState: &widgets.GridViewState{}}
	page.OnSearched("")
	if !grid.DisableSharing || page.searchText != "" || page.searchGridState != nil {
		t.Fatal("browse state not restored")
	}
	page.OnSearched("")
}
