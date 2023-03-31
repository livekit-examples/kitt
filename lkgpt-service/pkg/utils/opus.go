package utils

import (
	"errors"
	"time"
)

var (
	ErrInvalidPacket = errors.New("invalid opus packet")
)

// Parse the duration of a an OpusPacket
// https://www.rfc-editor.org/rfc/rfc6716#section-3.1
func ParsePacketDuration(data []byte) (time.Duration, error) {
	durations := [32]uint64{
		480, 960, 1920, 2880, // Silk-Only
		480, 960, 1920, 2880, // Silk-Only
		480, 960, 1920, 2880, // Silk-Only
		480, 960, // Hybrid
		480, 960, // Hybrid
		120, 240, 480, 960, // Celt-Only
		120, 240, 480, 960, // Celt-Only
		120, 240, 480, 960, // Celt-Only
		120, 240, 480, 960, // Celt-Only
	}

	if len(data) < 1 {
		return 0, ErrInvalidPacket
	}

	toc := data[0]
	var nframes int
	switch toc & 3 {
	case 0:
		nframes = 1
	case 1:
		nframes = 2
	case 2:
		nframes = 2
	case 3:
		if len(data) < 2 {
			return 0, ErrInvalidPacket
		}
		nframes = int(data[1] & 63)
	}

	frameDuration := int64(durations[toc>>3])
	duration := int64(nframes * int(frameDuration))
	if duration > 5760 { // 120ms
		return 0, ErrInvalidPacket
	}

	ms := duration * 1000 / 48000
	return time.Duration(ms) * time.Millisecond, nil
}
