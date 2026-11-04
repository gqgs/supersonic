package mediaprovider

import (
	"errors"
	"time"
)

// Expected artwork absence is different from authentication, decoding or server
// failures. Callers can keep a placeholder without reporting an application error.
var (
	ErrArtworkUnavailable = errors.New("artwork unavailable")
	ErrArtworkPending     = errors.New("artwork pending")
)

func IsExpectedArtworkError(err error) bool {
	return errors.Is(err, ErrArtworkUnavailable) || errors.Is(err, ErrArtworkPending)
}

// ArtworkRetryError carries a server cooldown without retaining a download slot.
type ArtworkRetryError struct {
	Err     error
	RetryAt time.Time
}

func (e *ArtworkRetryError) Error() string { return e.Err.Error() }
func (e *ArtworkRetryError) Unwrap() error { return e.Err }
