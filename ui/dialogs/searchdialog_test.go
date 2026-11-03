package dialogs

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"testing"
	"time"
)

// Suppress animation dispatch; result dispatch is explicitly controlled below.
type searchTestDriver struct{ fyne.Driver }

func (*searchTestDriver) DoFromGoroutine(func(), bool) {}

type searchTestApp struct {
	fyne.App
	driver fyne.Driver
}

func (a *searchTestApp) Driver() fyne.Driver { return a.driver }

func TestSearchDialogLatestQueryWins(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	fyne.SetCurrentApp(&searchTestApp{App: app, driver: &searchTestDriver{app.Driver()}})
	oldStarted, releaseOld := make(chan struct{}), make(chan struct{})
	sd := NewSearchDialog(nil, "Search", "Close", func(query string) []*mediaprovider.SearchResult {
		if query == "old" {
			close(oldStarted)
			<-releaseOld
		}
		return []*mediaprovider.SearchResult{{ID: query, Name: query}}
	})
	defer sd.loadingDots.Stop()
	queue := make(chan func(), 10)
	dispatch := func(f func()) { queue <- f }
	next := func() func() {
		t.Helper()
		select {
		case f := <-queue:
			return f
		case <-time.After(2 * time.Second):
			t.Fatal("search blocked")
			return nil
		}
	}
	sd.search("old", dispatch)
	<-oldStarted
	sd.search("new", dispatch)
	next()()
	if len(sd.searchResults) != 1 || sd.searchResults[0].ID != "new" {
		t.Fatal("new query was blocked by old request")
	}
	close(releaseOld)
	next()()
	if sd.searchResults[0].ID != "new" {
		t.Fatal("old completion replaced new results")
	}
	// A completed request queued before a new edit must also be rejected,
	// even before the new query's debounce timer expires.
	sd.search("queued", dispatch)
	obsolete := next()
	sd.searchEntry.SetText("typing")
	obsolete()
	if sd.searchResults[0].ID != "new" {
		t.Fatal("queued completion replaced results during debounce")
	}
	// Let the suppressed debounce callback finish before disposing the test app.
	time.Sleep(550 * time.Millisecond)
}

func TestQuickSearchEmptyQueryDoesNotFetchOrReuseResults(t *testing.T) {
	q := &QuickSearch{} // nil provider would panic if an empty query reached it
	if results := q.onSearched(""); len(results) != 0 {
		t.Fatal("empty query must clear results")
	}
}
