package subsonic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"

	"github.com/supersonic-app/supersonic/backend/mediaprovider"
)

const albumAudioQualityExtension = "appleMusicAlbumAudioQuality"
const maxAlbumAudioQualityResponseBytes = 64 * 1024

var _ mediaprovider.SupportsAlbumAudioQuality = (*subsonicMediaProvider)(nil)

// GetAlbumAudioQuality reports Apple Music catalog availability for this exact
// server album ID. It does not describe the stream or the local audio device.
func (s *subsonicMediaProvider) GetAlbumAudioQuality(albumID string) ([]string, error) {
	if albumID == "" {
		return nil, nil
	}
	// Do not change the shared client's response format: other requests may be in flight.
	client := *s.client
	client.UseJSON = true
	s.albumAudioQualityOnce.Do(func() {
		extensions, err := client.GetOpenSubsonicExtensions()
		if err != nil {
			return
		}
		for _, extension := range extensions {
			if extension != nil && extension.Name == albumAudioQualityExtension && slices.Contains(extension.Versions, 1) {
				s.albumAudioQualitySupported = true
				break
			}
		}
	})
	if !s.albumAudioQualitySupported {
		return nil, nil
	}
	response, err := client.Request(http.MethodGet, "getAlbumAudioQuality", url.Values{"id": {albumID}})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("album audio quality: HTTP status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAlbumAudioQualityResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAlbumAudioQualityResponseBytes {
		return nil, errors.New("album audio quality: response exceeds size limit")
	}
	var envelope struct {
		Response struct {
			Status  string          `json:"status"`
			Error   json.RawMessage `json:"error"`
			Quality *struct {
				ID        string   `json:"id"`
				Source    string   `json:"source"`
				Available []string `json:"available"`
			} `json:"albumAudioQuality"`
		} `json:"subsonic-response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("album audio quality: invalid response: %w", err)
	}
	result := envelope.Response
	if result.Status != "ok" || (len(result.Error) != 0 && string(result.Error) != "null") {
		return nil, errors.New("album audio quality: unsuccessful response")
	}
	quality := result.Quality
	if quality == nil || quality.ID != albumID || quality.Source != "apple-music-catalog" || quality.Available == nil {
		return nil, errors.New("album audio quality: invalid catalog metadata")
	}
	available := make([]string, 0, len(quality.Available))
	for _, value := range quality.Available {
		switch value {
		case "dolby-atmos", "dolby-audio", "hi-res-lossless", "lossless":
			if !slices.Contains(available, value) {
				available = append(available, value)
			}
		default:
			return nil, errors.New("album audio quality: unknown quality value")
		}
	}
	return available, nil
}
