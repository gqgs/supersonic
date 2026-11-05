package widgets

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"testing"
)

func TestGroupedReleasesSectionVisibility(t *testing.T) {
	g := &GroupedReleases{}
	for i := range g.sections {
		g.sections[i] = groupedReleasesSection{titleRow: &groupedReleasesHeader{}, container: &fyne.Container{}}
	}
	for collapsed := range g.sections {
		want := GroupedReleasesSectionVisibility{Albums: collapsed != 0, EPs: collapsed != 1, Singles: collapsed != 2, LiveAlbums: collapsed != 3, Compilations: collapsed != 4, Others: collapsed != 5}
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

func TestSeparateReleaseSectionsRenderCollapseAndHideEmptyGroups(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	model := GroupedReleasesModel{EPs: []GridViewItemModel{{ID: "ep", Name: "An EP"}}, Singles: []GridViewItemModel{{ID: "single", Name: "A Single"}}}
	group := NewGroupedReleases(model, nil)
	defer group.CancelImages()
	win := app.NewWindow("Release sections")
	defer win.Close()
	win.SetContent(group)
	win.Resize(fyne.NewSize(600, 600))
	group.Refresh()
	for i, want := range []string{"Albums", "EPs", "Singles", "Live Albums", "Compilations", "Others"} {
		section := group.sections[i]
		if section.titleRow.title != want {
			t.Fatalf("section %d=%s want %s", i, section.titleRow.title, want)
		}
		empty := i != 1 && i != 2
		if section.titleRow.Hidden != empty || section.container.Hidden != empty {
			t.Fatalf("empty group visibility incorrect: %s", want)
		}
	}
	if group.sections[1].titleRow.Position().Y >= group.sections[2].titleRow.Position().Y {
		t.Fatal("EPs not above Singles")
	}
	var opened string
	group.OnShowItemPage = func(id string) { opened = id }
	group.sections[1].container.Objects[0].(*GridViewItem).OnShowItemPage()
	if opened != "ep" {
		t.Fatal("EP card opens wrong release")
	}
	group.sections[1].titleRow.Tapped(nil)
	if !group.sections[1].container.Hidden || group.sections[2].container.Hidden {
		t.Fatal("EP collapse affected Singles")
	}
	saved := group.GetSectionVisibility()
	restored := NewGroupedReleases(model, nil)
	defer restored.CancelImages()
	restored.SetSectionVisibility(saved, true)
	if restored.GetSectionVisibility() != saved || !restored.sections[1].container.Hidden || restored.sections[2].container.Hidden {
		t.Fatal("navigation state did not preserve independent groups")
	}
	group.Model.EPs = nil
	group.Refresh()
	if !group.sections[1].titleRow.Hidden {
		t.Fatal("empty EP header remains visible")
	}
	group.Model.EPs = model.EPs
	group.Refresh()
	if group.sections[1].titleRow.Hidden || !group.sections[1].container.Hidden {
		t.Fatal("returning EPs lost collapse state")
	}
	if win.Canvas().Capture() == nil {
		t.Fatal("headless rendering failed")
	}
}
