package widgets

import (
	"context"
	"testing"
	"time"
)

type blockedGridIterator struct {
	started chan struct{}
	release chan struct{}
	id      string
	calls   int
}

func (i *blockedGridIterator) NextN(int) []GridViewItemModel {
	i.calls++
	close(i.started)
	<-i.release
	return []GridViewItemModel{{ID: i.id}}
}
func awaitGridTest(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("grid worker blocked")
	}
}

func TestGridSearchDiscardsCancelledResponseWithoutCancellingNewSearch(t *testing.T) {
	old := &blockedGridIterator{started: make(chan struct{}), release: make(chan struct{}), id: "old"}
	latest := &blockedGridIterator{started: make(chan struct{}), release: make(chan struct{}), id: "new"}
	oldCtx, oldCancel := context.WithCancel(context.Background())
	defer oldCancel()
	newCtx, newCancel := context.WithCancel(context.Background())
	defer newCancel()
	g := &GridView{GridViewState: GridViewState{iter: old}, fetchCancel: oldCancel, loadingDots: &LoadingDots{}}
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		g.fetchMoreItems(oldCtx, old, 6, func() { t.Error("stale result refreshed UI") })
	}()
	awaitGridTest(t, old.started)
	g.stateMutex.Lock()
	oldCancel()
	g.GridViewState = GridViewState{iter: latest}
	g.fetchCancel = newCancel
	g.stateMutex.Unlock()
	newDone := make(chan struct{})
	go func() { defer close(newDone); g.fetchMoreItems(newCtx, latest, 6, func() {}) }()
	awaitGridTest(t, latest.started)
	close(old.release)
	awaitGridTest(t, oldDone)
	g.stateMutex.Lock()
	if len(g.items) != 0 || g.done || g.fetchCancel == nil || newCtx.Err() != nil {
		t.Error("old response changed new search state")
	}
	g.stateMutex.Unlock()
	close(latest.release)
	awaitGridTest(t, newDone)
	items := g.Items()
	if len(items) != 1 || items[0].ID != "new" {
		t.Fatalf("items=%v", items)
	}
	if old.calls != 1 || latest.calls != 1 {
		t.Fatalf("request counts: old=%d new=%d", old.calls, latest.calls)
	}
}

func TestGridCancelledBeforeWorkerStartsDoesNotConsumeNewIterator(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	latest := &blockedGridIterator{started: make(chan struct{}), release: make(chan struct{})}
	g := &GridView{GridViewState: GridViewState{iter: latest}}
	done := make(chan struct{})
	go func() {
		defer close(done)
		g.fetchMoreItems(ctx, latest, 6, func() { t.Error("cancelled worker refreshed UI") })
	}()
	awaitGridTest(t, done)
	if latest.calls != 0 {
		t.Fatal("cancelled worker consumed results")
	}
}
