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
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Transcriber struct {
	ctx    context.Context
	cancel context.CancelFunc

	speechClient *stt.Client
	language     *Language

	rtpCodec webrtc.RTPCodecParameters
	//sb       *samplebuilder.SampleBuilder

	lock          sync.Mutex
	oggWriter     *io.PipeWriter
	oggReader     *io.PipeReader
	oggSerializer *oggwriter.OggWriter

	results chan RecognizeResult
	closeCh chan struct{}
}

type RecognizeResult struct {
	Error   error
	Text    string
	IsFinal bool
}

func NewTranscriber(rtpCodec webrtc.RTPCodecParameters, speechClient *stt.Client, language *Language) (*Transcriber, error) {
	if !strings.EqualFold(rtpCodec.MimeType, "audio/opus") {
		return nil, errors.New("only opus is supported")
	}

	oggReader, oggWriter := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	t := &Transcriber{
		ctx:      ctx,
		cancel:   cancel,
		rtpCodec: rtpCodec,
		//sb:           samplebuilder.New(200, &codecs.OpusPacket{}, rtpCodec.ClockRate),
		oggReader:    oggReader,
		oggWriter:    oggWriter,
		language:     language,
		speechClient: speechClient,
		results:      make(chan RecognizeResult),
		closeCh:      make(chan struct{}),
	}
	go t.start()
	return t, nil
}

func (t *Transcriber) Language() *Language {
	return t.language
}

func (t *Transcriber) WriteRTP(pkt *rtp.Packet) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	if t.oggSerializer == nil {
		oggSerializer, err := oggwriter.NewWith(t.oggWriter, t.rtpCodec.ClockRate, t.rtpCodec.Channels)
		if err != nil {
			logger.Errorw("failed to create ogg serializer", err)
			return err
		}
		t.oggSerializer = oggSerializer
	}

	//t.sb.Push(pkt)
	//for _, p := range t.sb.PopPackets() {
	if err := t.oggSerializer.WriteRTP(pkt); err != nil {
		return err
	}
	//}

	return nil
}

func (t *Transcriber) start() error {
	defer func() {
		close(t.closeCh)
	}()

	for {
		stream, err := t.newStream()
		if err != nil {
			if status, ok := status.FromError(err); ok && status.Code() == codes.Canceled {
				return nil
			}

			logger.Errorw("failed to create a new speech stream", err)
			t.results <- RecognizeResult{
				Error: err,
			}
			return err
		}

		endStreamCh := make(chan struct{})
		nextCh := make(chan struct{})

		// Forward oggreader to the speech stream
		go func() {
			defer close(nextCh)
			buf := make([]byte, 1024)
			for {
				select {
				case <-endStreamCh:
					return
				default:
					n, err := t.oggReader.Read(buf)
					if err != nil {
						if err != io.EOF {
							logger.Errorw("failed to read from ogg reader", err)
						}
						return
					}

					if n <= 0 {
						continue // No data
					}

					if err := stream.Send(&sttpb.StreamingRecognizeRequest{
						StreamingRequest: &sttpb.StreamingRecognizeRequest_AudioContent{
							AudioContent: buf[:n],
						},
					}); err != nil {
						if err != io.EOF {
							logger.Errorw("failed to forward audio data to speech stream", err)
							t.results <- RecognizeResult{
								Error: err,
							}
						}
						return
					}
				}
			}

		}()

		// Read transcription results
		for {
			resp, err := stream.Recv()
			if err != nil {
				if status, ok := status.FromError(err); ok {
					if status.Code() == codes.OutOfRange {
						break // Create a new speech stream (maximum speech length exceeded)
					} else if status.Code() == codes.Canceled {
						return nil // Context canceled (Stop)
					}
				}

				logger.Errorw("failed to receive response from speech stream", err)
				t.results <- RecognizeResult{
					Error: err,
				}

				return err
			}

			if resp.Error != nil {
				break
			}

			// Read the whole transcription and put inside one string
			// We don't need to process each part individually (atm?)
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

		// When nothing is written on the transcriber (The track is muted), this will block because the oggReader
		// is waiting for data. It avoids to create useless speech streams. (Also we end up here because Google automatically close the
		// previous stream when there's no "activity")
		//
		// Otherwise (When we have data) it is used to wait for the end of the current stream,
		// so we can create the next one and reset the oggSerializer
		<-nextCh

		// Create a new oggSerializer each time we open a new SpeechStream
		// This is required because the stream requires ogg headers to be sent again
		t.lock.Lock()
		t.oggSerializer = nil
		t.lock.Unlock()
	}
}

func (t *Transcriber) Close() {
	t.cancel()
	t.oggReader.Close()
	t.oggWriter.Close()
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
						{Value: "Kitt"},
						{Value: "Kit-t"},
						{Value: "Kit"},
					},
					Boost: 16,
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
						{Value: "Kite"},
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
		SampleRateHertz:   int32(t.rtpCodec.ClockRate),
		AudioChannelCount: int32(t.rtpCodec.Channels),
		LanguageCode:      t.language.TranscriberCode,
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
