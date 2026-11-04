package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPassthroughConfigDefaultsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[LocalPlayback]\nAudioDeviceName = 'alsa/hdmi:CARD=HDMI,DEV=3'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := ReadConfigFile(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.LocalPlayback.EAC3Passthrough {
		t.Fatal("legacy config enabled passthrough")
	}
	c.LocalPlayback.EAC3Passthrough = true
	c.LocalPlayback.EqualizerEnabled = true
	c.LocalPlayback.Volume = 37
	if err := c.WriteConfigFile(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := ReadConfigFile(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.LocalPlayback.EAC3Passthrough || loaded.LocalPlayback.AudioDeviceName != c.LocalPlayback.AudioDeviceName || !loaded.LocalPlayback.EqualizerEnabled || loaded.LocalPlayback.Volume != 37 {
		t.Fatal("settings not preserved")
	}
}
