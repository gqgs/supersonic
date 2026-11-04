package dialogs

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/supersonic-app/supersonic/backend"
	"github.com/supersonic-app/supersonic/backend/player/mpv"
	"strings"
	"testing"
)

func TestPassthroughSettingUsesConfiguredDeviceAndRequiresRestart(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	for _, local := range []bool{true, false} {
		config := backend.DefaultConfig("")
		config.LocalPlayback.AudioDeviceName = "alsa/hdmi:CARD=HDMI,DEV=3"
		s := &SettingsDialog{config: config, audioDevices: []mpv.AudioDevice{{Name: config.LocalPlayback.AudioDeviceName, Description: "HDMI"}}}
		tab := s.createPlaybackTab(local, true)
		var check *widget.Check
		var walk func(fyne.CanvasObject)
		walk = func(obj fyne.CanvasObject) {
			if c, ok := obj.(*widget.Check); ok && strings.HasPrefix(c.Text, "E-AC-3 passthrough") {
				check = c
			}
			if c, ok := obj.(*fyne.Container); ok {
				for _, child := range c.Objects {
					walk(child)
				}
			}
		}
		walk(tab.Content)
		if check == nil || check.Checked || check.Disabled() == local {
			t.Fatal("invalid passthrough control state")
		}
		if !strings.Contains(check.Text, "restart") {
			t.Fatal("missing restart requirement")
		}
		if local {
			check.SetChecked(true)
			if !config.LocalPlayback.EAC3Passthrough {
				t.Fatal("setting not saved")
			}
		}
		if config.LocalPlayback.AudioDeviceName != "alsa/hdmi:CARD=HDMI,DEV=3" {
			t.Fatal("device overwritten")
		}
	}
}
