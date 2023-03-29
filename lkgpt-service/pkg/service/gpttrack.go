package service

import (
	"errors"
	"io"
	"sync"
	"time"

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
		Channels: 1,
		MimeType: webrtc.MimeTypeOpus,
	}

	track, err := lksdk.NewLocalSampleTrack(cap)
	if err != nil {
		return nil, err
	}

	provider := &provider{}
	track.StartWrite(provider, func() {})

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

func (t *GPTTrack) QueueReader(reader *oggreader.OggReader) {
	t.provider.QueueReader(reader)
}

type provider struct {
	reader      *oggreader.OggReader
	lastGranule uint64

	queue []*oggreader.OggReader
	lock  sync.Mutex
}

func (p *provider) NextSample() (media.Sample, error) {
	if p.reader == nil && len(p.queue) > 0 {
		// Read the next ogg
		p.lock.Lock()
		p.reader = p.queue[0]
		p.queue = p.queue[1:]
		p.lock.Unlock()
	}

	if p.reader != nil {
		sample := media.Sample{}
		data, header, err := p.reader.ParseNextPage()
		if err != nil {
			if err == io.EOF {
				p.reader = nil
				return p.NextSample()
			} else {
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

		return sample, nil
	}

	// Otherwise send empty Opus frames
	return media.Sample{
		Data:     OpusSilenceFrame,
		Duration: 20 * time.Millisecond,
	}, nil
}

func (p *provider) QueueReader(reader *oggreader.OggReader) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.queue = append(p.queue, reader)
}

func (p *provider) OnBind() error {
	return nil
}

func (p *provider) OnUnbind() error {
	return nil
}
