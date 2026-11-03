package browsing

import (
	"fyne.io/fyne/v2/test"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"github.com/supersonic-app/supersonic/ui/controller"
	"github.com/supersonic-app/supersonic/ui/widgets"
	"testing"
)

type artistSearchProvider struct {
	mediaprovider.MediaProvider
	query string
}

func (p *artistSearchProvider) SearchArtists(query string, _ mediaprovider.ArtistFilter) mediaprovider.ArtistIterator {
	p.query = query
	return &oneArtist{}
}

type oneArtist struct{ done bool }

func (i *oneArtist) Next() *mediaprovider.Artist {
	if i.done {
		return nil
	}
	i.done = true
	return &mediaprovider.Artist{ID: "artist", Name: "Artist", AlbumCount: 10}
}

func TestArtistsSearchUIUsesArtistIteratorAndNavigation(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	provider := &artistSearchProvider{}
	var route controller.Route
	adapter := &artistsPageAdapter{mp: provider, contr: &controller.Controller{NavHandler: func(r controller.Route) { route = r }}}
	page := &GridViewPage[mediaprovider.Artist, mediaprovider.ArtistFilterOptions]{adapter: adapter}
	page.createSearchAndFilter()
	if page.searcher.PlaceHolder != "Search artists" {
		t.Fatalf("placeholder=%q", page.searcher.PlaceHolder)
	}
	results := adapter.SearchIter("artist", adapter.Filter()).NextN(6)
	if provider.query != "artist" || len(results) != 1 || results[0].ID != "artist" {
		t.Fatal("search did not return artist cards")
	}
	// Other provider methods are nil: a release fetch during search would panic.
	grid := &widgets.GridView{}
	adapter.InitGrid(grid)
	grid.OnShowItemPage(results[0].ID)
	if route != controller.ArtistRoute("artist") {
		t.Fatalf("click route=%v", route)
	}
}
