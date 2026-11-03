package subsonic

import (
	"log"
	"strconv"

	"github.com/supersonic-app/go-subsonic/subsonic"
)

const artistSearchPageSize = 50
const albumSearchPageSize = 50

type searchIterBase struct {
	artistsOnly   bool
	albumsOnly    bool
	musicFolderId string
	query         string
	artistOffset  int
	albumOffset   int
	songOffset    int
	s             *subsonic.Client
}

func (s *searchIterBase) fetchResults() *subsonic.SearchResult3 {
	searchOpts := map[string]string{
		"artistOffset": strconv.Itoa(s.artistOffset),
		"albumOffset":  strconv.Itoa(s.albumOffset),
		"songOffset":   strconv.Itoa(s.songOffset),
	}
	if s.artistsOnly {
		searchOpts["artistCount"] = strconv.Itoa(artistSearchPageSize)
		searchOpts["albumCount"] = "0"
		searchOpts["songCount"] = "0"
	}
	if s.albumsOnly {
		searchOpts["artistCount"] = "0"
		searchOpts["albumCount"] = strconv.Itoa(albumSearchPageSize)
		searchOpts["songCount"] = "0"
	}
	if s.musicFolderId != "" {
		searchOpts["musicFolderId"] = s.musicFolderId
	}
	results, err := s.s.Search3(s.query, searchOpts)
	if err != nil {
		log.Println(err)
		results = nil
	}
	if results == nil || (s.artistsOnly && len(results.Artist) == 0) || (s.albumsOnly && len(results.Album) == 0) || len(results.Album)+len(results.Artist)+len(results.Song) == 0 {
		return nil
	}
	return results
}
