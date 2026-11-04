package widgets

import (
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/supersonic-app/supersonic/backend"
	myTheme "github.com/supersonic-app/supersonic/ui/theme"
	"github.com/supersonic-app/supersonic/ui/util"
)

type GroupedReleasesModel struct {
	Albums        []GridViewItemModel
	SinglesAndEPs []GridViewItemModel
	LiveAlbums    []GridViewItemModel
	Compilations  []GridViewItemModel
	Others        []GridViewItemModel
}

type GroupedReleasesSectionVisibility struct {
	Albums        bool
	SinglesAndEPs bool
	LiveAlbums    bool
	Compilations  bool
	Others        bool
}

type GroupedReleases struct {
	widget.BaseWidget

	ShowSuffix bool

	Model GroupedReleasesModel

	OnPlay              func(id string, shuffle bool)
	OnPlayNext          func(id string)
	OnAddToQueue        func(id string)
	OnAddToPlaylist     func(id string)
	OnFavorite          func(id string, fav bool)
	OnDownload          func(id string)
	OnShare             func(id string)
	OnShowItemPage      func(id string)
	OnShowSecondaryPage func(id string)

	menu               *widget.PopUpMenu
	shareMenuItem      *fyne.MenuItem
	menuGridViewItemId string
	imageFetcher       util.ImageFetcher
	cardPool           sync.Pool

	sections [5]groupedReleasesSection

	container      *container.Scroll
	visibleArtwork map[*GridViewItem]bool
	artworkPaused  bool
}

type groupedReleasesSection struct {
	titleRow  *groupedReleasesHeader
	container *fyne.Container
}

func NewGroupedReleases(model GroupedReleasesModel, fetch util.ImageFetcher) *GroupedReleases {
	g := &GroupedReleases{
		Model:        model,
		imageFetcher: fetch,
	}
	g.cardPool.New = func() any { return g.createNewItemCard() }
	g.ExtendBaseWidget(g)

	cardSize := fyne.NewSquareSize(groupedCardSize())
	sections := []string{lang.L("Albums"), lang.L("Singles & EPs"), lang.L("Live Albums"), lang.L("Compilations"), lang.L("Others")}
	for i, s := range sections {
		_i := i
		g.sections[i].titleRow = newGroupedReleasesHeader(s, func(collapse bool) {
			if collapse {
				g.sections[_i].container.Hide()
			} else {
				g.sections[_i].container.Show()
			}
			g.Refresh()
		})
		g.sections[i].container = container.NewGridWrap(cardSize)
	}

	vbox := container.NewVBox()
	for i := range g.sections {
		vbox.Add(g.sections[i].titleRow)
		vbox.Add(g.sections[i].container)
	}
	g.container = container.NewVScroll(vbox)
	g.container.OnScrolled = func(fyne.Position) { g.updateVisibleArtwork() }

	return g
}

func (g *GroupedReleases) GetSectionVisibility() GroupedReleasesSectionVisibility {
	return GroupedReleasesSectionVisibility{
		Albums:        !g.sections[0].titleRow.Collapsed,
		SinglesAndEPs: !g.sections[1].titleRow.Collapsed,
		LiveAlbums:    !g.sections[2].titleRow.Collapsed,
		Compilations:  !g.sections[3].titleRow.Collapsed,
		Others:        !g.sections[4].titleRow.Collapsed,
	}
}

func (g *GroupedReleases) SetSectionVisibility(v GroupedReleasesSectionVisibility, refresh bool) {
	for i, b := range []bool{v.Albums, v.SinglesAndEPs, v.LiveAlbums, v.Compilations, v.Others} {
		g.sections[i].titleRow.Collapsed = !b
		g.sections[i].container.Hidden = !b
	}
	if refresh {
		g.Refresh()
	}
}

func (g *GroupedReleases) CreateRenderer() fyne.WidgetRenderer {
	return &groupedArtworkRenderer{WidgetRenderer: widget.NewSimpleRenderer(g.container), group: g}
}

func (g *GroupedReleases) GetScrollOffset() float32 {
	return g.container.Offset.Y
}

func (g *GroupedReleases) ScrollToOffset(offs float32) {
	g.container.ScrollToOffset(fyne.NewPos(0, offs))
	g.updateVisibleArtwork()
}

func (g *GroupedReleases) Refresh() {
	g.artworkPaused = false
	gridSize := groupedCardSize()
	sectionItems := [5][]GridViewItemModel{g.Model.Albums, g.Model.SinglesAndEPs, g.Model.LiveAlbums, g.Model.Compilations, g.Model.Others}
	layoutSize := fyne.NewSize(0, 0)
	for i, items := range sectionItems {
		lenItems := len(items)
		objects := g.sections[i].container.Objects
		// clear out excess cards in this section
		for x := lenItems; x < len(objects); x++ {
			card := objects[x].(*GridViewItem)
			card.ImgLoader.Cancel()
			delete(g.visibleArtwork, card)
			g.cardPool.Put(objects[x])
			objects[x] = nil
		}
		if len(objects) > lenItems {
			objects = objects[:lenItems]
		}

		// update existing cards
		for x := 0; x < len(objects); x++ {
			g.doUpdateItemCard(objects[x].(*GridViewItem), &items[x])
			objects[x].(*GridViewItem).SetSize(gridSize)
			if layoutSize.IsZero() {
				layoutSize = objects[x].MinSize()
			}
		}
		// append new ones as needed
		for x := len(objects); x < lenItems; x++ {
			card := g.cardPool.Get().(*GridViewItem)
			card.SetSize(gridSize)
			g.doUpdateItemCard(card, &items[x])
			objects = append(objects, card)
			if layoutSize.IsZero() {
				layoutSize = card.MinSize()
			}
		}

		l := layout.NewGridWrapLayout(layoutSize)
		g.sections[i].container.Layout = l
		// needed to initialize the col count so that MinSize is correct
		l.Layout(objects, g.Size())
		g.sections[i].container.Objects = objects
		// if section has no albums in it, hide
		if lenItems == 0 {
			g.sections[i].titleRow.Hide()
			g.sections[i].container.Hide()
		} else {
			g.sections[i].titleRow.Show()
			if !g.sections[i].titleRow.Collapsed {
				g.sections[i].container.Show()
			}
		}
	}
	g.BaseWidget.Refresh()
}

func (g *GroupedReleases) createNewItemCard() fyne.CanvasObject {
	card := NewGridViewItem(myTheme.AlbumIcon)
	card.SetSize(groupedCardSize())
	card.ItemIndex = -1
	card.ImgLoader = util.NewThumbnailLoader(g.imageFetcher, card.Cover.SetImage)
	card.ImgLoader.RequestSize = func() int { return util.ArtworkSize(card.Cover.Im) }
	card.ImgLoader.OnBeforeLoad = func() { card.Cover.SetImage(nil) }
	card.OnPlay = func() { g.onPlay(card.ItemID(), false) }
	card.OnFavorite = func(fav bool) {
		if g.OnFavorite != nil {
			g.OnFavorite(card.itemID, fav)
		}
	}
	card.OnShowSecondaryPage = func(id string) {
		if g.OnShowSecondaryPage != nil {
			g.OnShowSecondaryPage(id)
		}
	}
	card.OnShowItemPage = func() {
		if g.OnShowItemPage != nil {
			g.OnShowItemPage(card.ItemID())
		}
	}
	card.OnShowContextMenu = func(p fyne.Position) {
		g.showContextMenu(card, p)
	}
	return card
}

func (g *GroupedReleases) showContextMenu(card *GridViewItem, pos fyne.Position) {
	g.menuGridViewItemId = card.ItemID()
	if g.menu == nil {
		play := fyne.NewMenuItem(lang.L("Play"), func() { g.onPlay(g.menuGridViewItemId, false) })
		play.Icon = theme.MediaPlayIcon()
		shuffle := fyne.NewMenuItem(lang.L("Shuffle"), func() { g.onPlay(g.menuGridViewItemId, true) })
		shuffle.Icon = myTheme.ShuffleIcon
		queueNext := fyne.NewMenuItem(lang.L("Play next"), func() {
			if g.OnPlayNext != nil {
				g.OnPlayNext(g.menuGridViewItemId)
			}
		})
		queueNext.Icon = myTheme.PlayNextIcon
		queue := fyne.NewMenuItem(lang.L("Add to queue"), func() {
			if g.OnAddToQueue != nil {
				g.OnAddToQueue(g.menuGridViewItemId)
			}
		})
		queue.Icon = theme.ContentAddIcon()
		playlist := fyne.NewMenuItem(lang.L("Add to playlist")+"...", func() {
			if g.OnAddToPlaylist != nil {
				g.OnAddToPlaylist(g.menuGridViewItemId)
			}
		})
		playlist.Icon = myTheme.PlaylistIcon
		download := fyne.NewMenuItem(lang.L("Download")+"...", func() {
			if g.OnDownload != nil {
				g.OnDownload(g.menuGridViewItemId)
			}
		})
		download.Icon = theme.DownloadIcon()
		g.shareMenuItem = fyne.NewMenuItem(lang.L("Share")+"...", func() {
			g.OnShare(g.menuGridViewItemId)
		})
		g.shareMenuItem.Icon = myTheme.ShareIcon
		g.menu = widget.NewPopUpMenu(fyne.NewMenu("", play, shuffle, queueNext, queue, playlist, download, g.shareMenuItem),
			fyne.CurrentApp().Driver().CanvasForObject(g))
	}
	g.menu.ShowAtPosition(pos)
}

func (g *GroupedReleases) onPlay(itemID string, shuffle bool) {
	if g.OnPlay != nil {
		g.OnPlay(itemID, shuffle)
	}
}

func (g *GroupedReleases) doUpdateItemCard(card *GridViewItem, model *GridViewItemModel) {
	card.ShowSuffix = g.ShowSuffix
	if !card.NeedsUpdate(model) {
		// nothing to do
		return
	}

	card.Update(model)
	if g.container != nil {
		card.ImgLoader.Cancel()
		delete(g.visibleArtwork, card)
	} else {
		card.ImgLoader.Load(model.CoverArtID)
	}
}

type groupedReleasesHeader struct {
	widget.BaseWidget

	title    string
	onToggle func(bool)

	icon      *widget.Icon
	Collapsed bool
}

func newGroupedReleasesHeader(title string, onToggleVisibility func(bool)) *groupedReleasesHeader {
	g := &groupedReleasesHeader{
		title:    title,
		icon:     widget.NewIcon(theme.MenuDropDownIcon()),
		onToggle: onToggleVisibility,
	}
	g.ExtendBaseWidget(g)
	return g
}

func (g *groupedReleasesHeader) CreateRenderer() fyne.WidgetRenderer {
	titleText := widget.NewLabelWithStyle(g.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	titleText.SizeName = theme.SizeNameSubHeadingText
	return widget.NewSimpleRenderer(
		container.NewHBox(util.NewHSpace(8), titleText, util.NewHSpace(-theme.Padding()*2), g.icon),
	)
}

func (g *groupedReleasesHeader) Refresh() {
	if g.Collapsed {
		g.icon.Resource = theme.MenuExpandIcon()
	} else {
		g.icon.Resource = theme.MenuDropDownIcon()
	}
	g.BaseWidget.Refresh()
}

var _ fyne.Tappable = (*groupedReleasesHeader)(nil)

func (g *groupedReleasesHeader) Tapped(*fyne.PointEvent) {
	g.Collapsed = !g.Collapsed
	g.onToggle(g.Collapsed)
	g.Refresh()
}

// ReloadImages is an explicit retry, so repeated layout/paint refreshes do not
// create an unbounded network retry loop after failures.
func (g *GroupedReleases) ReloadImages() {
	if g.container != nil {
		g.CancelImages()
		g.artworkPaused = false
		g.updateVisibleArtwork()
		return
	}
	for _, section := range g.sections {
		for _, object := range section.container.Objects {
			card := object.(*GridViewItem)
			card.ImgLoader.Load(card.coverArtID)
		}
	}
}

func (g *GroupedReleases) CancelImages() {
	g.artworkPaused = true
	for card := range g.visibleArtwork {
		card.ImgLoader.Cancel()
	}
	g.visibleArtwork = nil
}

// Cards remain in the layout, but only intersecting rows consume network work.
func (g *GroupedReleases) updateVisibleArtwork() {
	if g.container == nil || g.artworkPaused {
		return
	}
	if g.visibleArtwork == nil {
		g.visibleArtwork = make(map[*GridViewItem]bool)
	}
	top, bottom := g.container.Offset.Y, g.container.Offset.Y+g.container.Size().Height
	visible := make(map[*GridViewItem]bool)
	for _, section := range g.sections {
		if section.container == nil || section.container.Hidden {
			continue
		}
		for _, object := range section.container.Objects {
			card := object.(*GridViewItem)
			y := section.container.Position().Y + card.Position().Y
			if bottom > top && y < bottom && y+card.Size().Height > top {
				visible[card] = true
			}
		}
	}
	// Release old rows before scheduling the newly visible ones.
	for card := range g.visibleArtwork {
		if !visible[card] {
			card.ImgLoader.Cancel()
			delete(g.visibleArtwork, card)
		}
	}
	for card := range visible {
		if !g.visibleArtwork[card] || card.ImgLoader.NeedsReload() {
			g.visibleArtwork[card] = true
			card.ImgLoader.Load(card.coverArtID)
		}
	}
}

type groupedArtworkRenderer struct {
	fyne.WidgetRenderer
	group *GroupedReleases
}

func (r *groupedArtworkRenderer) Layout(size fyne.Size) {
	r.WidgetRenderer.Layout(size)
	r.group.updateVisibleArtwork()
}
func (r *groupedArtworkRenderer) Refresh() {
	r.WidgetRenderer.Refresh()
	r.group.updateVisibleArtwork()
}

func groupedCardSize() float32 {
	if app := backend.AppInstance(); app != nil {
		return app.Config.GridView.CardSize
	}
	return 200
}
