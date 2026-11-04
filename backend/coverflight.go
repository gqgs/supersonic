package backend

import (
	"context"
	"github.com/google/uuid"
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"image"
)

type coverFlightKey struct {
	server     uuid.UUID
	id         string
	generation uint64
	size       int
}
type coverFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	img     image.Image
	err     error
}

// Only identical requests within the same server/cache generation share work.
// One departing card must not cancel other viewers; the last departure does.
func (i *ImageManager) sharedCover(ctx context.Context, id string, fetch func(context.Context) (image.Image, error)) (image.Image, error) {
	return i.sharedSizedCover(ctx, id, coverArtThumbnailSize, fetch)
}

func (i *ImageManager) sharedSizedCover(ctx context.Context, id string, size int, fetch func(context.Context) (image.Image, error)) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := coverFlightKey{i.s.ServerID, id, i.ImageCacheGeneration(), size}
	i.coverFlightsMu.Lock()
	if i.coverFlights == nil {
		i.coverFlights = make(map[coverFlightKey]*coverFlight)
	}
	flight := i.coverFlights[key]
	if flight == nil {
		work, cancel := context.WithCancel(context.Background())
		flight = &coverFlight{done: make(chan struct{}), cancel: cancel}
		i.coverFlights[key] = flight
		go func() {
			flight.img, flight.err = fetch(work)
			i.coverFlightsMu.Lock()
			if i.coverFlights[key] == flight {
				delete(i.coverFlights, key)
			}
			close(flight.done)
			i.coverFlightsMu.Unlock()
			cancel()
		}()
	}
	flight.waiters++
	i.coverFlightsMu.Unlock()
	defer func() {
		i.coverFlightsMu.Lock()
		defer i.coverFlightsMu.Unlock()
		flight.waiters--
		if flight.waiters == 0 {
			if i.coverFlights[key] == flight {
				delete(i.coverFlights, key)
			}
			flight.cancel()
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return flight.img, flight.err
	}
}

func fetchCoverWithContext(ctx context.Context, server mediaprovider.MediaProvider, id string, size int) (image.Image, error) {
	if provider, ok := server.(interface {
		GetCoverArtContext(context.Context, string, int) (image.Image, error)
	}); ok {
		return provider.GetCoverArtContext(ctx, id, size)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return server.GetCoverArt(id, size)
}
