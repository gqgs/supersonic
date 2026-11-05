package browsing

import (
	"fmt"
	"github.com/supersonic-app/supersonic/backend"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"github.com/supersonic-app/supersonic/ui/widgets"
	"testing"
)

func TestReleaseSection(t *testing.T) {
	tests := []struct {
		types mediaprovider.ReleaseTypes
		want  int
	}{
		{0, 0}, {mediaprovider.ReleaseTypeAlbum, 0},
		{mediaprovider.ReleaseTypeSingle, 2}, {mediaprovider.ReleaseTypeEP, 1},
		{mediaprovider.ReleaseTypeSingle | mediaprovider.ReleaseTypeEP, 1},
		{mediaprovider.ReleaseTypeAlbum | mediaprovider.ReleaseTypeLive, 3},
		{mediaprovider.ReleaseTypeAlbum | mediaprovider.ReleaseTypeCompilation, 4},
		{mediaprovider.ReleaseTypeOther, 5},
		{mediaprovider.ReleaseTypeAlbum | mediaprovider.ReleaseTypeOther, 5},
		{mediaprovider.ReleaseTypeLive | mediaprovider.ReleaseTypeCompilation | mediaprovider.ReleaseTypeEP, 3},
		{mediaprovider.ReleaseTypeCompilation | mediaprovider.ReleaseTypeEP, 4},
		{mediaprovider.ReleaseTypeSingle | mediaprovider.ReleaseTypeRemix, 2},
	}
	for _, typ := range []mediaprovider.ReleaseTypes{mediaprovider.ReleaseTypeAudiobook, mediaprovider.ReleaseTypeAudioDrama, mediaprovider.ReleaseTypeBroadcast, mediaprovider.ReleaseTypeDemo, mediaprovider.ReleaseTypeDJMix, mediaprovider.ReleaseTypeFieldRecording, mediaprovider.ReleaseTypeInterview, mediaprovider.ReleaseTypeMixtape, mediaprovider.ReleaseTypeRemix, mediaprovider.ReleaseTypeSoundtrack, mediaprovider.ReleaseTypeSpokenWord} {
		tests = append(tests, struct {
			types mediaprovider.ReleaseTypes
			want  int
		}{typ | mediaprovider.ReleaseTypeAlbum, 5})
	}
	for _, tt := range tests {
		if got := releaseSection(tt.types); got != tt.want {
			t.Errorf("types %x: got %d, want %d", tt.types, got, tt.want)
		}
	}
}

func TestGroupedReleasesLargeCatalogAndSorting(t *testing.T) {
	types := []mediaprovider.ReleaseTypes{mediaprovider.ReleaseTypeAlbum, mediaprovider.ReleaseTypeSingle, mediaprovider.ReleaseTypeEP, mediaprovider.ReleaseTypeLive, mediaprovider.ReleaseTypeCompilation, mediaprovider.ReleaseTypeOther}
	page := &ArtistPage{artistPageState: artistPageState{cfg: &backend.ArtistPageConfig{}}, artistInfo: &mediaprovider.ArtistWithAlbums{}}
	for i := 0; i < 120; i++ {
		year := 1900 + i
		page.artistInfo.Albums = append(page.artistInfo.Albums, &mediaprovider.Album{ID: fmt.Sprint(i), Name: fmt.Sprintf("Name %03d", 119-i), SortName: fmt.Sprintf("Sort %03d", i), Date: mediaprovider.ItemDate{Year: &year}, ReleaseTypes: types[i%len(types)]})
	}
	if !page.useGroupedReleases() {
		t.Fatal("large catalog must be grouped")
	}
	for _, sorting := range discographySorts {
		t.Run(sorting, func(t *testing.T) {
			page.cfg.DiscographySort = sorting
			model := page.getGroupedReleasesModel()
			groups := [][]widgets.GridViewItemModel{model.Albums, model.EPs, model.Singles, model.LiveAlbums, model.Compilations, model.Others}
			seen := map[string]bool{}
			for g, items := range groups {
				wantLen := 20
				if len(items) != wantLen {
					t.Fatalf("section %d: got %d releases, want %d", g, len(items), wantLen)
				}
				for i, item := range items {
					if seen[item.ID] {
						t.Fatalf("duplicate %s", item.ID)
					}
					seen[item.ID] = true
					if i > 0 {
						// SortName and year both ascend with the input index, unlike Name.
						var prev, cur int
						fmt.Sscan(items[i-1].ID, &prev)
						fmt.Sscan(item.ID, &cur)
						if sorting == discographySorts[1] && prev < cur || sorting != discographySorts[1] && prev > cur {
							t.Fatalf("wrong order: %s before %s", items[i-1].ID, item.ID)
						}
					}
				}
			}
			if len(seen) != 120 {
				t.Fatalf("lost releases: got %d", len(seen))
			}
		})
	}
}

func TestGroupingMissingMetadata(t *testing.T) {
	page := &ArtistPage{}
	if page.useGroupedReleases() {
		t.Fatal("nil artist grouped")
	}
	page.artistInfo = &mediaprovider.ArtistWithAlbums{}
	for i := 0; i < 100; i++ {
		page.artistInfo.Albums = append(page.artistInfo.Albums, &mediaprovider.Album{})
	}
	if page.useGroupedReleases() {
		t.Fatal("missing metadata must retain album grid")
	}
	page.artistInfo.Albums[0].ReleaseTypes = mediaprovider.ReleaseTypeOther
	if !page.useGroupedReleases() {
		t.Fatal("explicit Other must enable grouping")
	}
	page.cfg = &backend.ArtistPageConfig{}
	model := page.getGroupedReleasesModel()
	if len(model.Albums) != 99 || len(model.Others) != 1 {
		t.Fatal("missing and explicit Other were conflated")
	}
}

func TestGroupedReleasesNameFallback(t *testing.T) {
	page := &ArtistPage{artistPageState: artistPageState{cfg: &backend.ArtistPageConfig{DiscographySort: discographySorts[2]}}, artistInfo: &mediaprovider.ArtistWithAlbums{Albums: []*mediaprovider.Album{
		{ID: "z", Name: "Zulu", ReleaseTypes: mediaprovider.ReleaseTypeEP},
		{ID: "a", Name: "Alpha", ReleaseTypes: mediaprovider.ReleaseTypeEP},
		{ID: "s", Name: "The Aardvarks", SortName: "Aardvarks", ReleaseTypes: mediaprovider.ReleaseTypeEP},
	}}}
	got := page.getGroupedReleasesModel().EPs
	for i, want := range []string{"s", "a", "z"} {
		if got[i].ID != want {
			t.Fatalf("item %d: got %s, want %s", i, got[i].ID, want)
		}
	}
}

func TestSeparateEPsAndSinglesUseMetadataWithoutDuplicates(t *testing.T) {
	page := &ArtistPage{artistPageState: artistPageState{cfg: &backend.ArtistPageConfig{}}, artistInfo: &mediaprovider.ArtistWithAlbums{Albums: []*mediaprovider.Album{
		{ID: "both", Name: "Ambiguous", ReleaseTypes: mediaprovider.ReleaseTypeEP | mediaprovider.ReleaseTypeSingle},
		{ID: "ep", Name: "A Single", ReleaseTypes: mediaprovider.ReleaseTypeEP},
		{ID: "single", Name: "An EP", ReleaseTypes: mediaprovider.ReleaseTypeSingle},
		{ID: "missing", Name: "Title - EP"},
		{ID: "other", Name: "Another Single", ReleaseTypes: mediaprovider.ReleaseTypeOther},
	}}}
	model := page.getGroupedReleasesModel()
	if len(model.EPs) != 2 || len(model.Singles) != 1 || model.Singles[0].ID != "single" || len(model.Albums) != 1 || model.Albums[0].ID != "missing" || len(model.Others) != 1 {
		t.Fatalf("incorrect metadata grouping: %+v", model)
	}
	seen := map[string]bool{}
	for _, group := range [][]widgets.GridViewItemModel{model.Albums, model.EPs, model.Singles, model.LiveAlbums, model.Compilations, model.Others} {
		for _, item := range group {
			if seen[item.ID] {
				t.Fatalf("duplicated %s", item.ID)
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Fatal("lost release")
	}
}
