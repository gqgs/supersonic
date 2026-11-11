package backend

import (
	"context"
	"log"
	"math"
	"sync"

	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

type LyricsManager struct {
	sm     *ServerManager
	lrclib lyricsFallback

	// right now only one song can have lyrics being fetched
	// at any given time (b/c we only show lyrics for the currently playing song)
	lock                  sync.Mutex
	fetchInProgressID     string
	fetchInProgressCancel context.CancelFunc
	cbs                   []func(string, *mediaprovider.Lyrics)
}

type lyricsFallback interface {
	FetchLrcLibLyrics(name, artist, album string, durationSecs int) (*mediaprovider.Lyrics, error)
}

func NewLyricsManager(sm *ServerManager, lrclib *LrcLibFetcher) *LyricsManager {
	lm := &LyricsManager{sm: sm}
	if lrclib != nil {
		lm.lrclib = lrclib
	}
	return lm
}

func (lm *LyricsManager) FetchLyricsAsync(song *mediaprovider.Track, cb func(string, *mediaprovider.Lyrics)) {
	lm.lock.Lock()
	defer lm.lock.Unlock()

	if lm.fetchInProgressID == song.ID {
		lm.cbs = append(lm.cbs, cb)
		return
	}
	lm.fetchInProgressID = song.ID

	if lm.fetchInProgressCancel != nil {
		lm.fetchInProgressCancel()
	}
	lm.cbs = []func(string, *mediaprovider.Lyrics){cb}
	ctx, cancel := context.WithCancel(context.Background())
	lm.fetchInProgressCancel = cancel
	go lm.fetchLyrics(ctx, song, func(id string, lyrics *mediaprovider.Lyrics) {
		lm.lock.Lock()
		// A newer request may have replaced this one after fetchLyrics checked cancellation.
		if ctx.Err() != nil {
			lm.lock.Unlock()
			return
		}
		callbacks := lm.cbs
		lm.fetchInProgressID = ""
		lm.fetchInProgressCancel()
		lm.cbs = nil
		// Callbacks must not run under the manager lock (they can request another song).
		lm.lock.Unlock()
		for _, cb := range callbacks {
			cb(id, lyrics)
		}
	})
}

func (lm *LyricsManager) fetchLyrics(ctx context.Context, song *mediaprovider.Track, cb func(string, *mediaprovider.Lyrics)) {
	var lyrics *mediaprovider.Lyrics
	var err error
	if lp, ok := lm.sm.Server.(mediaprovider.LyricsProvider); ok {
		if lyrics, err = lp.GetLyrics(song); err != nil {
			log.Printf("Error fetching lyrics: %v", err)
		}
	}
	if !hasTimedLyrics(lyrics) && lm.lrclib != nil && ctx.Err() == nil {
		artist := ""
		if len(song.ArtistNames) > 0 {
			artist = song.ArtistNames[0]
		}
		fallback, fallbackErr := lm.lrclib.FetchLrcLibLyrics(song.Title, artist, song.Album, int(song.Duration.Seconds()))
		err = fallbackErr
		lyrics = preferTimedLyrics(lyrics, fallback)
		if err != nil {
			log.Println(err.Error())
		}
	}
	select {
	case <-ctx.Done():
		return
	default:
		cb(song.ID, lyrics)
	}
}

// Keep server text when the optional lookup misses or only offers another plain copy.
func preferTimedLyrics(primary, fallback *mediaprovider.Lyrics) *mediaprovider.Lyrics {
	if fallback != nil && len(fallback.Lines) > 0 && (primary == nil || hasTimedLyrics(fallback)) {
		return fallback
	}
	return primary
}

func hasTimedLyrics(lyrics *mediaprovider.Lyrics) bool {
	if lyrics == nil || !lyrics.Synced || len(lyrics.Lines) == 0 {
		return false
	}
	previous := -1.0
	for _, line := range lyrics.Lines {
		if math.IsNaN(line.Start) || math.IsInf(line.Start, 0) || line.Start < 0 || line.Start < previous {
			return false
		}
		previous = line.Start
	}
	return true
}
