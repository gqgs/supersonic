package subsonic

import (
	"fmt"
	"image"
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
	return s.getCoverArtWithDelays(id, size, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second})
}

func (s *subsonicMediaProvider) getCoverArtWithDelays(id string, size int, delays []time.Duration) (image.Image, error) {
	var request func() (*http.Response, error)
	if strings.HasPrefix(id, artistImageURLPrefix) {
		imageURL := strings.TrimPrefix(id, artistImageURLPrefix)
		u, err := url.Parse(imageURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid artist image URL")
		}
		// External URLs must not receive Subsonic authentication parameters.
		request = func() (*http.Response, error) { return s.client.Client.Get(imageURL) }
	} else {
		params := url.Values{"id": {id}}
		if size > 0 {
			params.Set("size", strconv.Itoa(size))
		}
		request = func() (*http.Response, error) { return s.client.Request(http.MethodGet, "getCoverArt", params) }
	}
	for attempt := 0; ; attempt++ {
		resp, err := request()
		if err != nil {
			return nil, err
		}
		img, retry, err := decodeCoverResponse(resp)
		resp.Body.Close()
		if !retry || attempt == len(delays) {
			return img, err
		}
		time.Sleep(delays[attempt])
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
