package sharedutil

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// audioDownloadError retains error identity without exposing URLs, response
// bodies, or filesystem paths in logs (stream URLs can contain credentials).
type audioDownloadError struct {
	operation string
	cause     error
}

func (e *audioDownloadError) Error() string { return e.operation }
func (e *audioDownloadError) Unwrap() error { return e.cause }

func audioDownloadFailure(ctx context.Context, operation string, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	switch {
	case errors.Is(err, context.Canceled):
		operation += ": canceled"
	case errors.Is(err, context.DeadlineExceeded):
		operation += ": deadline exceeded"
	case errors.Is(err, io.ErrUnexpectedEOF):
		operation += ": truncated response"
	}
	return &audioDownloadError{operation: operation, cause: err}
}

// DownloadAudioFileWithContext downloads an audio response, rejecting error
// documents even when a server labels them as audio or application/octet-stream.
// It does not require a particular codec, container, or requested transcode
// profile. A false result always has an error; unsuccessful writes are removed.
func DownloadAudioFileWithContext(ctx context.Context, url, destPath string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, audioDownloadFailure(ctx, "creating audio request failed", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, audioDownloadFailure(ctx, "requesting audio failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("audio request returned HTTP %d", resp.StatusCode)
	}

	// Bound sniffing independently of response size, preserving transport errors
	// (including unexpected EOF) rather than treating them as short audio files.
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return false, audioDownloadFailure(ctx, "reading audio response failed", err)
	}
	n := len(prefix)
	if ctx.Err() != nil {
		return false, audioDownloadFailure(ctx, "reading audio response failed", ctx.Err())
	}
	if n == 0 {
		return false, errors.New("audio response is empty")
	}
	if resp.ContentLength >= 0 && n < 512 && int64(n) < resp.ContentLength {
		return false, audioDownloadFailure(ctx, "reading audio response failed", io.ErrUnexpectedEOF)
	}
	if audioResponseIsDocument(prefix) {
		return false, errors.New("audio response contains a text or error document")
	}

	out, err := os.Create(destPath)
	if err != nil {
		return false, audioDownloadFailure(ctx, "creating audio file failed", err)
	}
	complete := false
	defer func() {
		out.Close()
		if !complete {
			os.Remove(destPath)
		}
	}()
	written, err := io.Copy(out, io.MultiReader(bytes.NewReader(prefix), resp.Body))
	if err != nil {
		return false, audioDownloadFailure(ctx, "writing audio response failed", err)
	}
	if ctx.Err() != nil {
		return false, audioDownloadFailure(ctx, "writing audio response failed", ctx.Err())
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		return false, audioDownloadFailure(ctx, "writing audio response failed", io.ErrUnexpectedEOF)
	}
	if err := out.Close(); err != nil {
		return false, audioDownloadFailure(ctx, "closing audio file failed", err)
	}
	complete = true
	return true, nil
}

func audioResponseIsDocument(prefix []byte) bool {
	// Recognized media signatures take priority over misleading MIME types and
	// printable metadata. This is an exception list, not a codec allowlist:
	// unrecognized binary responses remain supported.
	for _, magic := range []string{"ID3", "fLaC", "OggS", "RIFF", "RF64", "FORM", "MAC ", "wvpk", "MPCK", "DSD ", "FRM8", "#!AMR"} {
		if bytes.HasPrefix(prefix, []byte(magic)) {
			return false
		}
	}
	if len(prefix) >= 8 && (string(prefix[4:8]) == "ftyp" || string(prefix[4:8]) == "moov" || string(prefix[4:8]) == "mdat") {
		return false
	}
	// Validate the text following a Unicode BOM, not just the BOM itself:
	// ff fe can also start a valid MPEG Layer I frame.
	if audioUnicodeDocument(prefix) {
		return true
	}
	if len(prefix) >= 2 && (prefix[0] == 0xff && prefix[1]&0xe0 == 0xe0 || prefix[0] == 0x0b && prefix[1] == 0x77) {
		return false
	}
	prefix = bytes.TrimPrefix(prefix, []byte{0xef, 0xbb, 0xbf})
	// Require a textual prefix throughout, rather than rejecting arbitrary
	// binary formats whose first byte happens to be '<', '{', or '['.
	for len(prefix) > 0 {
		r, size := utf8.DecodeRune(prefix)
		if r == utf8.RuneError && size == 1 {
			// The bounded prefix may end midway through a valid UTF-8 character.
			return !utf8.FullRune(prefix)
		}
		if !unicode.IsPrint(r) && !unicode.IsSpace(r) {
			return false
		}
		prefix = prefix[size:]
	}
	return true
}

// audioUnicodeDocument checks a bounded UTF-16/32 prefix. At least two valid
// ASCII text characters must follow the BOM, followed by printable Unicode.
// Requiring an ASCII opening avoids treating MPEG bytes as random CJK text.
func audioUnicodeDocument(prefix []byte) bool {
	var order binary.ByteOrder
	width := 2
	switch {
	case bytes.HasPrefix(prefix, []byte{0xff, 0xfe, 0, 0}):
		order, width, prefix = binary.LittleEndian, 4, prefix[4:]
	case bytes.HasPrefix(prefix, []byte{0, 0, 0xfe, 0xff}):
		order, width, prefix = binary.BigEndian, 4, prefix[4:]
	case bytes.HasPrefix(prefix, []byte{0xff, 0xfe}):
		order, prefix = binary.LittleEndian, prefix[2:]
	case bytes.HasPrefix(prefix, []byte{0xfe, 0xff}):
		order, prefix = binary.BigEndian, prefix[2:]
	default:
		return false
	}
	var runes []rune
	if width == 2 {
		var units []uint16
		for len(prefix) >= 2 {
			units = append(units, order.Uint16(prefix))
			prefix = prefix[2:]
		}
		runes = utf16.Decode(units)
	} else {
		for len(prefix) >= 4 {
			runes = append(runes, rune(order.Uint32(prefix)))
			prefix = prefix[4:]
		}
	}
	if len(runes) < 2 || runes[0] > unicode.MaxASCII || runes[1] > unicode.MaxASCII {
		return false
	}
	for _, r := range runes {
		if r == utf8.RuneError || (!unicode.IsPrint(r) && !unicode.IsSpace(r)) {
			return false
		}
	}
	return true
}
