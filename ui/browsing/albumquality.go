package browsing

import (
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

// Catalog availability is deliberately separate from the active playback format.
func formatAlbumAudioQuality(available []string) string {
	var labels []string
	if slices.Contains(available, "dolby-atmos") {
		labels = append(labels, "Dolby Atmos")
	} else if slices.Contains(available, "dolby-audio") {
		labels = append(labels, "Dolby Audio")
	}
	if slices.Contains(available, "hi-res-lossless") {
		labels = append(labels, "Hi-Res Lossless")
	} else if slices.Contains(available, "lossless") {
		labels = append(labels, "Lossless")
	}
	return strings.Join(labels, " · ")
}

func (a *AlbumPageHeader) clearAudioQuality() {
	a.qualityGeneration++
	a.qualityLabel.SetText("")
	a.qualityLabel.Hide()
}

func (a *AlbumPage) loadAudioQuality(generation uint64) {
	provider, ok := a.mp.(mediaprovider.SupportsAlbumAudioQuality)
	if !ok {
		return
	}
	header := a.header
	revision := header.qualityGeneration
	albumID := a.albumID
	go func() {
		available, err := provider.GetAlbumAudioQuality(albumID)
		if err != nil {
			// Optional catalog metadata must never interrupt browsing.
			return
		}
		fyne.Do(func() {
			a.applyAudioQuality(generation, revision, albumID, available)
		})
	}()
}

// Called only on the UI goroutine, including when an asynchronous result arrives.
func (a *AlbumPage) applyAudioQuality(generation, revision uint64, albumID string, available []string) {
	if !a.loadIsCurrent(generation) || a.header.qualityGeneration != revision ||
		a.albumID != albumID || a.header.albumID != albumID {
		return
	}
	text := formatAlbumAudioQuality(available)
	if text != "" {
		text = lang.L("Available") + ": " + text
	}
	a.header.qualityLabel.SetText(text)
	a.header.Refresh()
}
