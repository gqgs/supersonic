package mpv

import (
	"fmt"
	"strings"
)

// SetEAC3Passthrough is startup-only to avoid interrupting an active device or
// changing the decoder/filter chain underneath playback. Restart applies it.
func (p *Player) SetEAC3Passthrough(enabled bool) error {
	if p.initialized {
		return fmt.Errorf("changing E-AC-3 passthrough requires a restart")
	}
	p.eac3Passthrough = enabled
	return nil
}

type optionSetter interface{ SetOptionString(string, string) error }

func configurePassthrough(m optionSetter, enabled bool) error {
	codecs := ""
	if enabled {
		codecs = "eac3"
	}
	if err := m.SetOptionString("audio-spdif", codecs); err != nil {
		return fmt.Errorf("setting E-AC-3 passthrough: %w", err)
	}
	return nil
}

// This reports the format at the audio API, not what a receiver decoded.
// Neither the source codec nor passthrough proves the presence of Atmos metadata.
func (m MediaInfo) OutputDescription() string {
	if m.OutputFormat == "" {
		return "Unknown"
	}
	if strings.HasPrefix(m.OutputFormat, "spdif-") {
		return strings.ToUpper(strings.TrimPrefix(m.OutputFormat, "spdif-")) + " passthrough"
	}
	return "PCM (" + m.OutputFormat + ")"
}
