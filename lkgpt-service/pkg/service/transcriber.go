package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"

	stt "cloud.google.com/go/speech/apiv1"
	sttpb "cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/server-sdk-go/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

type Transcriber struct {
	ctx    context.Context
	cancel context.CancelFunc

	track        *webrtc.TrackRemote
	speechClient *stt.Client
	language     *Language

	onTranscription func(resp *sttpb.StreamingRecognizeResponse)
	lock            sync.Mutex

	closedChan chan struct{}
}

func NewTranscriber(track *webrtc.TrackRemote, speechClient *stt.Client, language *Language) (*Transcriber, error) {
	rtpCodec := track.Codec()

	if !strings.EqualFold(rtpCodec.MimeType, "audio/opus") {
		return nil, errors.New("only opus is supported")
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Transcriber{
		ctx:          ctx,
		cancel:       cancel,
		track:        track,
		language:     language,
		speechClient: speechClient,
		closedChan:   make(chan struct{}),
	}, nil
}

func (t *Transcriber) OnTranscriptionReceived(f func(resp *sttpb.StreamingRecognizeResponse)) {
	t.lock.Lock()
	t.onTranscription = f
	t.lock.Unlock()
}

func (t *Transcriber) Start() error {
	oggReader, pw := io.Pipe()
	sampleBuilder := samplebuilder.New(200, &codecs.OpusPacket{}, t.track.Codec().ClockRate)

	go func() {
		// Read RTP packets from the track
		oggWriter, err := oggwriter.NewWith(pw, t.track.Codec().ClockRate, t.track.Codec().Channels)
		if err != nil {
			logger.Errorw("failed to create ogg writer", err)
			return
		}

		for {
			select {
			case <-t.closedChan:
				return
			default:
				pkt, _, err := t.track.ReadRTP()
				if err != nil {
					return
				}

				sampleBuilder.Push(pkt)
				for _, p := range sampleBuilder.PopPackets() {
					oggWriter.WriteRTP(p)
				}
			}
		}
	}()

streamLoop:
	for {
		logger.Infow("creating a new speech stream")

		closeChan := make(chan struct{})
		stream, err := t.newStream()
		if err != nil {
			return err
		}

		go func() {
			// Forward track packets to the speech stream
			buf := make([]byte, 1024)
		forwardLoop:
			for {
				select {
				case <-closeChan:
					break
				default:
					n, err := oggReader.Read(buf)
					if err != nil {
						if err != io.EOF {
							logger.Errorw("failed to read from ogg reader", err)
						}
						break forwardLoop
					}

					if n <= 0 {
						// No data
						continue
					}

					// Forward to speech stream
					if err := stream.Send(&sttpb.StreamingRecognizeRequest{
						StreamingRequest: &sttpb.StreamingRecognizeRequest_AudioContent{
							AudioContent: buf[:n],
						},
					}); err != nil {
						logger.Errorw("failed to send audio content to speech stream", err)
						break forwardLoop
					}
				}
			}
		}()

		for {
			// Read transcription results
			resp, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					// Create a new speech stream
					break
				}

				logger.Errorw("failed to receive response from speech stream", err)
				break streamLoop
			}
			// TODO(theomonnom): Use channel
			t.lock.Lock()
			onTranscription := t.onTranscription
			t.lock.Unlock()

			if onTranscription != nil {
				onTranscription(resp)
			}

		}

		close(closeChan)
	}

	close(t.closedChan)
	return nil
}

func (t *Transcriber) Stop() {
	t.cancel()
	<-t.closedChan
}

func (t *Transcriber) newStream() (sttpb.Speech_StreamingRecognizeClient, error) {
	stream, err := t.speechClient.StreamingRecognize(t.ctx)
	if err != nil {
		return nil, err
	}

	config := &sttpb.RecognitionConfig{
		Model: "command_and_search",
		Adaptation: &sttpb.SpeechAdaptation{
			PhraseSets: []*sttpb.PhraseSet{
				{
					Phrases: []*sttpb.PhraseSet_Phrase{
						{Value: "${hello} ${gpt}"},
						{Value: "${gpt}"},
						{Value: "Hey ${gpt}"},
					},
					Boost: 19,
				},
			},
			CustomClasses: []*sttpb.CustomClass{
				{
					CustomClassId: "hello",
					Items: []*sttpb.CustomClass_ClassItem{
						{Value: "Hi"},
						{Value: "Hello"},
						{Value: "Hey"},
					},
				},
				{
					CustomClassId: "gpt",
					Items: []*sttpb.CustomClass_ClassItem{
						{Value: "GPT"},
						{Value: "Live Kit"},
						{Value: "Live GPT"},
						{Value: "LiveKit"},
						{Value: "LiveGPT"},
						{Value: "Live-Kit"},
						{Value: "Live-GPT"},
					},
				},
			},
		},
		UseEnhanced:       true,
		Encoding:          sttpb.RecognitionConfig_OGG_OPUS,
		SampleRateHertz:   int32(t.track.Codec().ClockRate),
		AudioChannelCount: int32(t.track.Codec().Channels),
		LanguageCode:      t.language.Code,
	}

	if err := stream.Send(&sttpb.StreamingRecognizeRequest{
		StreamingRequest: &sttpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &sttpb.StreamingRecognitionConfig{
				InterimResults: true,
				Config:         config,
			},
		},
	}); err != nil {
		return nil, err
	}

	return stream, nil
}
