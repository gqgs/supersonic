package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"testing"
	"time"
)

type searchTestDriver struct {
	fyne.Driver
	queue chan func()
}

func (d *searchTestDriver) DoFromGoroutine(f func(), _ bool) { d.queue <- f }

type searchTestApp struct {
	fyne.App
	driver fyne.Driver
}

func (a *searchTestApp) Driver() fyne.Driver { return a.driver }

func TestSearchEntryDebouncesTypingAndClearsImmediately(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	queue := make(chan func(), 100)
	fyne.SetCurrentApp(&searchTestApp{App: app, driver: &searchTestDriver{Driver: app.Driver(), queue: queue}})
	entry := NewSearchEntry()
	var queries []string
	entry.OnSearched = func(s string) { queries = append(queries, s) }
	entry.SetText("r")
	// A pause longer than the former 200 ms should no longer issue a request.
	select {
	case <-queue:
		t.Fatal("search fired before 500 ms")
	case <-time.After(250 * time.Millisecond):
	}
	entry.SetText("radiohead")
	select {
	case <-queue:
		t.Fatal("typing did not reset debounce")
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case f := <-queue:
		f()
	case <-time.After(2 * time.Second):
		t.Fatal("search did not fire")
	}
	if len(queries) != 1 || queries[0] != "radiohead" {
		t.Fatalf("queries: %v", queries)
	}
	entry.SetText("old")
	var obsolete func()
	select {
	case obsolete = <-queue:
	case <-time.After(2 * time.Second):
		t.Fatal("search did not queue")
	}
	entry.SetText("")
	if len(queries) != 2 || queries[1] != "" {
		t.Fatalf("clear was not immediate: %v", queries)
	}
	obsolete()
	select {
	case f := <-queue:
		f()
	case <-time.After(2 * time.Second):
		t.Fatal("clear debounce did not finish")
	}
	if len(queries) != 2 {
		t.Fatalf("obsolete or duplicate query emitted: %v", queries)
	}
}
