package subsonic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

type coverAPIError struct {
	Code    int    `xml:"code,attr" json:"code"`
	Message string `xml:"message,attr" json:"message"`
}

func (e *coverAPIError) Error() string {
	return fmt.Sprintf("Subsonic artwork error %d: %s", e.Code, e.Message)
}

type coverErrorEnvelope struct {
	Status string         `xml:"status,attr" json:"status"`
	Error  *coverAPIError `xml:"error" json:"error"`
}

// Inspect bytes, not the MIME label: some servers mislabel valid JPEG/PNG data.
// Only API error envelopes are parsed; image streams remain untouched.
func readCoverAPIError(reader *bufio.Reader) (*coverAPIError, error) {
	prefix, _ := reader.Peek(512)
	prefix = bytes.TrimSpace(bytes.TrimPrefix(prefix, []byte{0xef, 0xbb, 0xbf}))
	isXML := bytes.HasPrefix(prefix, []byte("<")) && bytes.Contains(prefix, []byte("<subsonic-response"))
	isJSON := bytes.HasPrefix(prefix, []byte("{")) && bytes.Contains(prefix, []byte(`"subsonic-response"`))
	if !isXML && !isJSON {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(reader, 65537))
	if err != nil {
		return nil, err
	}
	if len(data) > 65536 {
		return nil, fmt.Errorf("Subsonic artwork error response too large")
	}
	var envelope coverErrorEnvelope
	if isXML {
		err = xml.Unmarshal(data, &envelope)
	} else {
		var document struct {
			Response coverErrorEnvelope `json:"subsonic-response"`
		}
		err = json.Unmarshal(data, &document)
		envelope = document.Response
	}
	if err != nil {
		return nil, fmt.Errorf("invalid Subsonic artwork response: %w", err)
	}
	if envelope.Status != "failed" || envelope.Error == nil {
		return nil, fmt.Errorf("Subsonic cover endpoint returned an API response without image data")
	}
	return envelope.Error, nil
}

func expectedCoverError(resp *http.Response, apiError *coverAPIError) (bool, error) {
	// Never hide an authentication/protocol failure behind an unavailable header.
	if apiError != nil && apiError.Code != 0 && apiError.Code != 70 {
		return false, apiError
	}
	if apiError != nil && apiError.Code == 70 {
		return false, fmt.Errorf("%w: %v", mediaprovider.ErrArtworkUnavailable, apiError)
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, fmt.Errorf("%w: HTTP 404", mediaprovider.ErrArtworkUnavailable)
	}
	if resp.StatusCode == http.StatusAccepted {
		return true, mediaprovider.ErrArtworkPending
	}
	if resp.StatusCode == http.StatusServiceUnavailable && resp.Header.Get("X-Artwork-Status") == "unavailable" {
		switch resp.Header.Get("X-Artwork-Reason") {
		case "release-pending", "release-busy", "lookup-pending", "queue-full":
			return true, fmt.Errorf("%w: %s", mediaprovider.ErrArtworkPending, resp.Header.Get("X-Artwork-Reason"))
		case "release-unavailable":
			return false, mediaprovider.ErrArtworkUnavailable
		}
	}
	if apiError != nil {
		return retryCoverStatus(resp.StatusCode), apiError
	}
	return false, nil
}

func coverRetryAfter(resp *http.Response) time.Duration {
	value := resp.Header.Get("Retry-After")
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		if seconds > 3600 {
			seconds = 3600
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return time.Until(date)
	}
	return 0
}

type coverCooldownKey struct {
	id   string
	size int
}
type coverCooldown struct {
	until time.Time
	err   error
}

func (s *subsonicMediaProvider) coverCooldownError(id string, size int) error {
	s.coverCooldownMu.Lock()
	defer s.coverCooldownMu.Unlock()
	key := coverCooldownKey{id, size}
	entry := s.coverCooldowns[key]
	if time.Now().Before(entry.until) {
		return entry.err
	}
	delete(s.coverCooldowns, key)
	return nil
}
func (s *subsonicMediaProvider) rememberCoverCooldown(id string, size int, delay time.Duration, err error) {
	if delay <= 0 || !mediaprovider.IsExpectedArtworkError(err) {
		return
	}
	s.coverCooldownMu.Lock()
	defer s.coverCooldownMu.Unlock()
	if s.coverCooldowns == nil {
		s.coverCooldowns = make(map[coverCooldownKey]coverCooldown)
	}
	if len(s.coverCooldowns) >= 256 {
		for key := range s.coverCooldowns {
			delete(s.coverCooldowns, key)
			break
		}
	}
	s.coverCooldowns[coverCooldownKey{id, size}] = coverCooldown{time.Now().Add(delay), err}
}

func retryCoverStatus(status int) bool {
	return status == http.StatusAccepted || status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable || status == http.StatusBadGateway || status == http.StatusGatewayTimeout
}
