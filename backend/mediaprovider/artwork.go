package mediaprovider

import "errors"

// Expected artwork absence is different from authentication, decoding or server
// failures. Callers can keep a placeholder without reporting an application error.
var (
	ErrArtworkUnavailable = errors.New("artwork unavailable")
	ErrArtworkPending     = errors.New("artwork pending")
)

func IsExpectedArtworkError(err error) bool {
	return errors.Is(err, ErrArtworkUnavailable) || errors.Is(err, ErrArtworkPending)
}
