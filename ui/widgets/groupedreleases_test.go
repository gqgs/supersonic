package widgets

import (
	"fyne.io/fyne/v2"
	"testing"
)

func TestGroupedReleasesSectionVisibility(t *testing.T) {
	g := &GroupedReleases{}
	for i := range g.sections {
		g.sections[i] = groupedReleasesSection{titleRow: &groupedReleasesHeader{}, container: &fyne.Container{}}
	}
	for collapsed := range g.sections {
		want := GroupedReleasesSectionVisibility{Albums: collapsed != 0, SinglesAndEPs: collapsed != 1, LiveAlbums: collapsed != 2, Compilations: collapsed != 3, Others: collapsed != 4}
		g.SetSectionVisibility(want, false)
		if got := g.GetSectionVisibility(); got != want {
			t.Fatalf("visibility roundtrip: got %+v, want %+v", got, want)
		}
		for i, section := range g.sections {
			if section.container.Hidden != (i == collapsed) {
				t.Fatalf("section %d hidden=%v", i, section.container.Hidden)
			}
		}
	}
}
