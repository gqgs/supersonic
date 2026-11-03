package res

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

func TestAlbumCountTranslations(t *testing.T) {
	entries, err := Translations.ReadDir("translations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := Translations.ReadFile("translations/" + entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			tag := language.Make(strings.TrimSuffix(entry.Name(), ".json"))
			bundle := i18n.NewBundle(tag)
			bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
			if _, err := bundle.ParseMessageFileBytes(data, entry.Name()); err != nil {
				t.Fatal(err)
			}
			localizer := i18n.NewLocalizer(bundle, tag.String())
			for _, count := range []int{1, 2, 5, 11, 21, 22, 101, 1000000} {
				label, err := localizer.Localize(&i18n.LocalizeConfig{
					MessageID: "{{.albumsCount}} albums", PluralCount: count,
					TemplateData: map[string]string{"albumsCount": strconv.Itoa(count)},
				})
				if err != nil {
					t.Errorf("count %d: %v", count, err)
				}
				if !strings.Contains(label, strconv.Itoa(count)) || strings.Contains(label, "{{") {
					t.Errorf("count %d: invalid label %q", count, label)
				}
				if entry.Name() == "pl.json" && count == 2 && label != "2 albumy" {
					t.Errorf("Polish few: %q", label)
				}
				if entry.Name() == "ro.json" && count == 22 && label != "22 de albume" {
					t.Errorf("Romanian other: %q", label)
				}
			}
		})
	}
}
