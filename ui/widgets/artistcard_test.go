package widgets

import (
	"bytes"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/test"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"github.com/supersonic-app/supersonic/res"
	"log"
	"testing"
)

type artistCardIterator struct{ artist *mediaprovider.Artist }

func (i *artistCardIterator) Next() *mediaprovider.Artist { a := i.artist; i.artist = nil; return a }
func TestArtistCardsDoNotInventCountsOrArtwork(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	data, err := res.Translations.ReadFile("translations/en.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := lang.AddTranslations(fyne.NewStaticResource(lang.SystemLocale().LanguageString()+".json", data)); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousOutput)
	defer func() {
		if bytes.Contains(logs.Bytes(), []byte("Translation failure")) {
			t.Errorf("translation error: %s", logs.String())
		}
	}()
	for _, count := range []int{-1, 0, 1, 23} {
		iter := NewGridViewArtistIterator(&artistCardIterator{artist: &mediaprovider.Artist{ID: "a", Name: "Artist", AlbumCount: count}})
		card := iter.NextN(1)[0]
		if count <= 0 && len(card.Secondary) != 0 {
			t.Fatalf("unknown/zero count displayed: %v", card.Secondary)
		}
		if count > 0 {
			want := fmt.Sprintf("%d albums", count)
			if count == 1 {
				want = "1 album"
			}
			if len(card.Secondary) != 1 || card.Secondary[0] != want {
				t.Fatalf("known count: %v, want %q", card.Secondary, want)
			}
		}
		if card.CoverArtID != "" {
			t.Fatal("invented artwork")
		}
	}
}
