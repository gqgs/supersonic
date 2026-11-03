package subsonic

import (
	"github.com/supersonic-app/supersonic/backend/mediaprovider"
	"testing"
)

func TestNormalizeReleaseTypes(t *testing.T) {
	tests := []struct {
		name  string
		types []string
		want  mediaprovider.ReleaseTypes
	}{
		{"missing", nil, mediaprovider.ReleaseTypeAlbum},
		{"empty", []string{}, mediaprovider.ReleaseTypeAlbum},
		{"unknown", []string{"future-type"}, mediaprovider.ReleaseTypeAlbum},
		{"album", []string{"Album"}, mediaprovider.ReleaseTypeAlbum},
		{"ep", []string{"EP"}, mediaprovider.ReleaseTypeEP},
		{"single", []string{"Single"}, mediaprovider.ReleaseTypeSingle},
		{"live", []string{"Live"}, mediaprovider.ReleaseTypeLive},
		{"compilation", []string{"Compilation"}, mediaprovider.ReleaseTypeCompilation},
		{"other", []string{"Other"}, mediaprovider.ReleaseTypeOther},
		{"unknown with other", []string{"future-type", "Other"}, mediaprovider.ReleaseTypeOther},
		{"single and ep", []string{"EP", "Single"}, mediaprovider.ReleaseTypeEP | mediaprovider.ReleaseTypeSingle},
		{"audiobook", []string{"Audiobook"}, mediaprovider.ReleaseTypeAudiobook},
		{"drama", []string{"Audio drama"}, mediaprovider.ReleaseTypeAudioDrama},
		{"broadcast", []string{"Broadcast"}, mediaprovider.ReleaseTypeBroadcast},
		{"demo", []string{"Demo"}, mediaprovider.ReleaseTypeDemo},
		{"dj mix", []string{"DJ-mix"}, mediaprovider.ReleaseTypeDJMix},
		{"field recording", []string{"Field recording"}, mediaprovider.ReleaseTypeFieldRecording},
		{"interview", []string{"Interview"}, mediaprovider.ReleaseTypeInterview},
		{"mixtape", []string{"Mixtape/Street"}, mediaprovider.ReleaseTypeMixtape},
		{"legacy mixtape", []string{"Mixtape"}, mediaprovider.ReleaseTypeMixtape},
		{"remix", []string{"Remix"}, mediaprovider.ReleaseTypeRemix},
		{"soundtrack", []string{"Soundtrack"}, mediaprovider.ReleaseTypeSoundtrack},
		{"spoken word", []string{"Spokenword"}, mediaprovider.ReleaseTypeSpokenWord},
		{"combined", []string{" Album ", "LIVE", "Compilation", "Live"}, mediaprovider.ReleaseTypeAlbum | mediaprovider.ReleaseTypeLive | mediaprovider.ReleaseTypeCompilation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeReleaseTypes(tt.types); got != tt.want {
				t.Fatalf("got %x, want %x", got, tt.want)
			}
		})
	}
}
