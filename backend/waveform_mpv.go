package backend

/*
#cgo darwin CFLAGS: -I/opt/homebrew/include -I/opt/local/include
#cgo darwin LDFLAGS: -L/opt/homebrew/lib -L/opt/local/lib
#if defined(__has_include) && __has_include(<libmpv/client.h>)
#include <libmpv/client.h>
#else
#include <mpv/client.h>
#endif
*/
import "C"

import (
	"errors"

	"github.com/supersonic-app/go-mpv"
)

// go-mpv exposes the end-file payload as an untyped pointer. Read the public
// libmpv struct, not a guessed Go memory layout. Never include MPV log messages
// or input URLs in the returned error.
func waveformEndFileError(event *mpv.Event) error {
	if event.Error != nil {
		return errors.New("waveform audio decoding failed")
	}
	if event.Data == nil {
		return nil
	}
	end := (*C.mpv_event_end_file)(event.Data)
	if end.reason == C.MPV_END_FILE_REASON_ERROR || end.error < 0 {
		return errors.New("waveform audio decoding failed")
	}
	return nil
}
