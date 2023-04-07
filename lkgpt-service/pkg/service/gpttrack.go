package service

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/livekit-examples/livegpt/pkg/utils"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

var (
	ErrMuted         = errors.New("the track is muted")
	ErrInvalidFormat = errors.New("invalid format")

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
	OpusSilenceFrameDuration = 20 * time.Millisecond
)

type GPTTrack struct {
	sampleTrack *lksdk.LocalSampleTrack
	provider    *provider

	doneChan   chan struct{}
	closedChan chan struct{}
}

func NewGPTTrack() (*GPTTrack, error) {
	cap := webrtc.RTPCodecCapability{
		Channels:  1,
		MimeType:  webrtc.MimeTypeOpus,
		ClockRate: 48000,
	}

	track, err := lksdk.NewLocalSampleTrack(cap)
	if err != nil {
		return nil, err
	}

	provider := &provider{}
	err = track.StartWrite(provider, func() {})
	if err != nil {
		return nil, err
	}

	return &GPTTrack{
		sampleTrack: track,
		provider:    provider,
		doneChan:    make(chan struct{}),
		closedChan:  make(chan struct{}),
	}, nil
}

func (t *GPTTrack) Publish(lp *lksdk.LocalParticipant) (pub *lksdk.LocalTrackPublication, err error) {
	pub, err = lp.PublishTrack(t.sampleTrack, &lksdk.TrackPublicationOptions{})
	return
}

// Called when the last oggReader in the queue finished being read
func (t *GPTTrack) OnComplete(f func(err error)) {
	t.provider.OnComplete(f)
}

func (t *GPTTrack) QueueReader(reader io.Reader) error {
	oggReader, oggHeader, err := utils.NewOggReader(reader)
	if err != nil {
		return err
	}

	// oggHeader.SampleRate is _not_ the sample rate to use for playback.
	// see https://www.rfc-editor.org/rfc/rfc7845.html#section-3
	if oggHeader.Channels != 1 /*|| oggHeader.SampleRate != 48000*/ {
		return ErrInvalidFormat
	}

	t.provider.QueueReader(oggReader)
	return nil
}

type provider struct {
	reader      *utils.OggReader
	lastGranule uint64

	queue      []*utils.OggReader
	lock       sync.Mutex
	onComplete func(err error)
}

func (p *provider) NextSample() (media.Sample, error) {
	p.lock.Lock()
	onComplete := p.onComplete
	if p.reader == nil && len(p.queue) > 0 {
		p.lastGranule = 0
		p.reader = p.queue[0]
		p.queue = p.queue[1:]
	}
	p.lock.Unlock()

	if p.reader != nil {
		data, err := p.reader.ReadPacket()
		if err != nil {
			if onComplete != nil {
				onComplete(err)
			}

			if err == io.EOF {
				p.reader = nil
				return p.NextSample()
			} else {
				logger.Errorw("failed to parse next page", err)
				return media.Sample{}, err
			}
		}

		duration, err := utils.ParsePacketDuration(data)
		if err != nil {
			return media.Sample{}, err
		}

		return media.Sample{
			Data:     data,
			Duration: duration,
		}, nil
	}

	// Otherwise send empty Opus frames
	return media.Sample{
		Data:     OpusSilenceFrame,
		Duration: OpusSilenceFrameDuration,
	}, nil
}

func (p *provider) OnBind() error {
	return nil
}

func (p *provider) OnUnbind() error {
	return nil
}

// Called when the *one* oggReader finished reading
func (t *provider) OnComplete(f func(err error)) {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.onComplete = f
}

func (p *provider) QueueReader(reader *utils.OggReader) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.queue = append(p.queue, reader)
}
