package service

import (
	"sync/atomic"
	"time"

	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

var (
	OpusSilenceFrame = []byte{
		0xf8, 0xff, 0xfe, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
)

type GPTTrack struct {
	sampleTrack       *lksdk.LocalSampleTrack
	activeEmptyPacket atomic.Bool

	doneChan   chan struct{}
	closedChan chan struct{}
}

func NewGPTTrack() (*GPTTrack, error) {
	track, err := lksdk.NewLocalSampleTrack(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus})
	if err != nil {
		return nil, err
	}

	return &GPTTrack{
		sampleTrack: track,
	}, nil
}

func (t *GPTTrack) Publish(lp *lksdk.LocalParticipant) (pub *lksdk.LocalTrackPublication, err error) {
	pub, err = lp.PublishTrack(t.sampleTrack, &lksdk.TrackPublicationOptions{})
	return
}

func (t *GPTTrack) SetMuted(muted bool) {
	t.activeEmptyPacket.Store(muted)
}

func (t *GPTTrack) Start() {
	emptyPacketTicker := time.NewTicker(20 * time.Millisecond)
loop:
	for {
		select {
		case <-t.doneChan:
			break loop
		case <-emptyPacketTicker.C:
			if !t.sampleTrack.IsBound() {
				continue
			}

			// Send empty OpusPacket
			if t.activeEmptyPacket.Load() {
				err := t.sampleTrack.WriteSample(media.Sample{Data: OpusSilenceFrame}, nil)
				if err != nil {
					logger.Warnw("failed to send empty frame", err)
					continue
				}
			}
			break
		}
	}

	close(t.closedChan)
}

func (t *GPTTrack) Stop() {
	close(t.doneChan)
	<-t.closedChan
}
