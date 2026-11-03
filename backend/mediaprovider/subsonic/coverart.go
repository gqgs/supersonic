package subsonic

import (
	"context"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Keep the server cache policy attached to the decoded image all the way to
// ImageManager. A no-store response can contain genuine artwork.
type uncacheableImage struct{ image.Image }

func (uncacheableImage) Cacheable() bool { return false }

func (s *subsonicMediaProvider) GetCoverArt(id string, size int) (image.Image, error) {
	return s.GetCoverArtContext(context.Background(), id, size)
}

func (s *subsonicMediaProvider) GetCoverArtContext(ctx context.Context, id string, size int) (image.Image, error) {
	return s.getCoverArtContext(ctx, id, size, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second})
}
func (s *subsonicMediaProvider) getCoverArtWithDelays(id string, size int, delays []time.Duration) (image.Image, error) {
	return s.getCoverArtContext(context.Background(), id, size, delays)
}
func (s *subsonicMediaProvider) getCoverArtContext(ctx context.Context, id string, size int, delays []time.Duration) (image.Image, error) {
	client := *s.client
	httpClient := *s.client.Client
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	httpClient.Transport = coverContextTransport{ctx, transport}
	client.Client = &httpClient
	var request func() (*http.Response, error)
	if strings.HasPrefix(id, artistImageURLPrefix) {
		imageURL := strings.TrimPrefix(id, artistImageURLPrefix)
		u, err := url.Parse(imageURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid artist image URL")
		}
		// External URLs must not receive Subsonic authentication parameters.
		request = func() (*http.Response, error) { return client.Client.Get(imageURL) }
	} else {
		params := url.Values{"id": {id}}
		if size > 0 {
			params.Set("size", strconv.Itoa(size))
		}
		request = func() (*http.Response, error) { return client.Request(http.MethodGet, "getCoverArt", params) }
	}
	var waitBudget time.Duration
	for _, delay := range delays {
		waitBudget += delay
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resp, err := request()
		if err != nil {
			return nil, err
		}
		img, retry, err := decodeCoverResponse(resp)
		resp.Body.Close()
		if !retry || attempt == len(delays) {
			return img, err
		}
		delay := delays[attempt]
		if value := resp.Header.Get("Retry-After"); value != "" {
			var serverDelay time.Duration
			if seconds, parseErr := strconv.Atoi(value); parseErr == nil && seconds > 0 {
				// Bound conversion before multiplying an untrusted header by a duration.
				if seconds > 3600 {
					seconds = 3600
				}
				serverDelay = time.Duration(seconds) * time.Second
			} else if date, parseErr := http.ParseTime(value); parseErr == nil {
				serverDelay = time.Until(date)
			}
			if serverDelay > delay {
				delay = serverDelay
			}
		}
		if delay > waitBudget {
			return img, fmt.Errorf("%w (retry after %s)", err, delay)
		}
		waitBudget -= delay
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func decodeCoverResponse(resp *http.Response) (image.Image, bool, error) {
	if resp.StatusCode != http.StatusOK {
		retry := resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusGatewayTimeout
		return nil, retry, fmt.Errorf("cover image returned HTTP %d", resp.StatusCode)
	}
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	// Some servers mislabel binary artwork. Let the decoder inspect the bytes,
	// just as the original client did, before classifying an error response.
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		if strings.Contains(contentType, "json") || strings.Contains(contentType, "xml") || strings.HasPrefix(contentType, "text/") {
			return nil, false, fmt.Errorf("cover image returned non-image content type %q: %w", contentType, err)
		}
		return nil, false, fmt.Errorf("decoding cover image: %w", err)
	}
	for _, directive := range strings.Split(resp.Header.Get("Cache-Control"), ",") {
		if strings.EqualFold(strings.TrimSpace(directive), "no-store") {
			// Cache policy is not a pending-resolution signal. Only transient
			// HTTP statuses trigger retries; return successful artwork immediately.
			return uncacheableImage{img}, false, nil
		}
	}
	return img, false, nil
}

// The pinned API client has no context-aware Request method. Attach cancellation
// at its transport boundary, preserving its authentication and HTTP timeout.
type coverContextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t coverContextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	cleanup := func() { stop(); cancel() }
	if t.ctx.Err() != nil {
		cleanup()
		return nil, t.ctx.Err()
	}
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cleanup()
		return nil, err
	}
	resp.Body = &coverResponseBody{ReadCloser: resp.Body, cleanup: cleanup}
	return resp, nil
}

type coverResponseBody struct {
	io.ReadCloser
	cleanup func()
}

func (b *coverResponseBody) Close() error { defer b.cleanup(); return b.ReadCloser.Close() }

// These namespaces identify expensive artist-card resolution in the companion
// bridge, or a direct artist image URL. Ordinary server cover IDs stay unclassified.
func (s *subsonicMediaProvider) IsArtistCoverArt(id string) bool {
	return strings.HasPrefix(id, "artist-image:") || strings.HasPrefix(id, artistImageURLPrefix)
}
