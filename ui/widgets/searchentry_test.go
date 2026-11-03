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
	next := func() func() {
		t.Helper()
		select {
		case f := <-queue:
			return f
		case <-time.After(3 * time.Second):
			t.Fatal("search did not dispatch")
			return nil
		}
	}
	started := time.Now()
	entry.SetText("r")
	queued := next()
	if time.Since(started) < 500*time.Millisecond {
		t.Fatal("search fired before 500 ms")
	}
	// A timer may already have fired when the next edit arrives. Its queued
	// callback must be discarded, not treated as a search for the new text.
	started = time.Now()
	entry.SetText("radiohead")
	queued()
	if len(queries) != 0 {
		t.Fatalf("stale timer emitted a search: %v", queries)
	}
	next()()
	if time.Since(started) < 500*time.Millisecond {
		t.Fatal("typing did not reset debounce")
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
