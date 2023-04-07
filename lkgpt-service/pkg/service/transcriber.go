package service

import (
	"context"
	"errors"
	"io"
	"strings"

	stt "cloud.google.com/go/speech/apiv1"
	sttpb "cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/server-sdk-go/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Transcriber struct {
	ctx    context.Context
	cancel context.CancelFunc

	track        *webrtc.TrackRemote
	speechClient *stt.Client
	language     *Language

	results chan RecognizeResult
	closeCh chan struct{}
}

type RecognizeResult struct {
	Error   error
	Text    string
	IsFinal bool
}

func NewTranscriber(track *webrtc.TrackRemote, speechClient *stt.Client, language *Language) (*Transcriber, error) {
	rtpCodec := track.Codec()

	if !strings.EqualFold(rtpCodec.MimeType, "audio/opus") {
		return nil, errors.New("only opus is supported")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t := &Transcriber{
		ctx:          ctx,
		cancel:       cancel,
		track:        track,
		language:     language,
		speechClient: speechClient,
		results:      make(chan RecognizeResult),
		closeCh:      make(chan struct{}),
	}
	go t.start()
	return t, nil
}

func (t *Transcriber) start() error {
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
			case <-t.closeCh:
				return
			default:
				pkt, _, err := t.track.ReadRTP()
				if err != nil {
					if err != io.EOF {
						logger.Errorw("failed to read from track", err)
					}
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
		logger.Debugw("creating a new speech stream")
		endStreamCh := make(chan struct{})
		stream, err := t.newStream()
		if err != nil {
			return err
		}

		go func() {
			// Forward track packets to the speech stream
			buf := make([]byte, 1024)
			for {
				select {
				case <-endStreamCh:
					return
				default:
					n, err := oggReader.Read(buf)
					if err != nil {
						if err != io.EOF {
							logger.Errorw("failed to read from ogg reader", err)
						}
						return
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
						if err != io.EOF {
							logger.Errorw("failed to send audio content to speech stream", err)
							t.results <- RecognizeResult{
								Error: err,
							}
						}
						return
					}
				}
			}
		}()

		for {
			// Read transcription results
			resp, err := stream.Recv()
			if err != nil {
				if status, ok := status.FromError(err); ok {
					if status.Code() == codes.OutOfRange {
						// Create a new speech stream (maximum speech length exceeded)
						break
					} else if status.Code() == codes.Canceled {
						// Context canceled (Stop)
						break streamLoop
					}
				}

				logger.Errorw("failed to receive response from speech stream", err)
				t.results <- RecognizeResult{
					Error: err,
				}

				break streamLoop
			}

			if resp.Error != nil {
				t.results <- RecognizeResult{
					Error: status.FromProto(resp.Error).Err(),
				}
				continue
			}

			var sb strings.Builder
			final := false
			for _, result := range resp.Results {
				alt := result.Alternatives[0]
				text := alt.Transcript
				sb.WriteString(text)

				if result.IsFinal {
					sb.Reset()
					sb.WriteString(text)
					final = true
					break
				}
			}

			t.results <- RecognizeResult{
				Text:    sb.String(),
				IsFinal: final,
			}
		}

		close(endStreamCh)
	}

	close(t.closeCh)
	return nil
}

func (t *Transcriber) Close() {
	t.cancel()
	<-t.closeCh
	close(t.results)
}

func (t *Transcriber) Results() <-chan RecognizeResult {
	return t.results
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
						{Value: "Kit"},
						{Value: "KITT"},
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
