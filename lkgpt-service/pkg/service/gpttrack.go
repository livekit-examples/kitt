package service

import (
	"errors"
	"io"
	"sync"
	"time"

	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/oggreader"
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

	DefaultOpusFrameDuration = 20 * time.Millisecond
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
	oggReader, oggHeader, err := oggreader.NewWith(reader)
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
	reader      *oggreader.OggReader
	lastGranule uint64

	queue      []*oggreader.OggReader
	lock       sync.Mutex
	onComplete func(err error)
}

func (p *provider) NextSample() (media.Sample, error) {
	p.lock.Lock()
	onComplete := p.onComplete
	if p.reader == nil && len(p.queue) > 0 {
		logger.Debugw("switching to next reader")
		p.lastGranule = 0
		p.reader = p.queue[0]
		p.queue = p.queue[1:]
	}
	p.lock.Unlock()

	if p.reader != nil {
		sample := media.Sample{}
		data, header, err := p.reader.ParseNextPage()
		if err != nil {
			if onComplete != nil {
				onComplete(err)
			}

			if err == io.EOF {
				p.reader = nil
				return p.NextSample()
			} else {
				logger.Errorw("failed to parse next page", err)
				return sample, err
			}
		}

		sampleCount := float64(header.GranulePosition - p.lastGranule)
		p.lastGranule = header.GranulePosition

		sample.Data = data
		sample.Duration = time.Duration((sampleCount/48000)*1000) * time.Millisecond
		if sample.Duration == 0 {
			sample.Duration = DefaultOpusFrameDuration
		}
		logger.Debugw("got sample", "duration", sample.Duration, "size", len(sample.Data))

		return sample, nil
	}

	// Otherwise send empty Opus frames
	return media.Sample{
		Data:     OpusSilenceFrame,
		Duration: 20 * time.Millisecond,
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

func (p *provider) QueueReader(reader *oggreader.OggReader) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.queue = append(p.queue, reader)
}
