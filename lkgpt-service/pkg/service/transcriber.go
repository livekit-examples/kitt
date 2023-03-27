package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/livekit/server-sdk-go/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"

	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

// To achieve endless streaming speech recognition, we need to split the transcribe requests to GCP,
// otherwise, the duration is limited to ~5 minutes.
// https://cloud.google.com/speech-to-text/docs/endless-streaming-tutorial
//
// When the speech stream is available:
// 1. Listen to RTP packets
// 2. Sample opus packets from the RTP packets
// 3. Write the opus packets to an OGG container
// 4. Send the OGG data to GCP
//
// Otherwise:
// 1. Try to recreate a new speech stream
// 2. Wait 4.5 minutes and close the stream

const MaxSpeechStreamDuration = 4 * time.Minute

type Transcriber struct {
	track         *webrtc.TrackRemote
	sampleBuilder *samplebuilder.SampleBuilder
	oggReader     *io.PipeReader
	oggWriter     *oggwriter.OggWriter
	speechClient  *speech.Client
}

func NewTranscriber(track *webrtc.TrackRemote, speechClient *speech.Client) (*Transcriber, error) {
	rtpCodec := track.Codec()

	if !strings.EqualFold(rtpCodec.MimeType, "audio/opus") {
		return nil, errors.New("only opus is supported")
	}

	sampleBuilder := samplebuilder.New(200, &codecs.OpusPacket{}, rtpCodec.ClockRate)
	oggReader, pw := io.Pipe()
	oggWriter, err := oggwriter.NewWith(pw, rtpCodec.ClockRate, rtpCodec.Channels)

	if err != nil {
		return nil, err
	}

	return &Transcriber{
		track:         track,
		sampleBuilder: sampleBuilder,
		oggReader:     oggReader,
		oggWriter:     oggWriter,
		speechClient:  speechClient,
	}, nil
}

func (t *Transcriber) start() {
	ctx := context.Background()
	buf := make([]byte, 1024)
	rtpCodec := t.track.Codec()

	for {
		var wg sync.WaitGroup
		ctx, _ := context.WithTimeout(ctx, MaxSpeechStreamDuration)

		speech, err := newSpeechStream(ctx, t.speechClient, rtpCodec)
		if err != nil {
			fmt.Println("failed to create a new speech stream %v", err)
			return
		}

		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				err := t.forwardSpeech(buf, speech)
				if err != nil {
					fmt.Println("failed to forward speech %v", err)
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()

			// Read the response from GCP
			for {
				resp, err := speech.Recv()
				if err != nil {
					fmt.Println("Cannot stream results: %v", err)
				}

				for _, result := range resp.Results {
					fmt.Printf("Result: %+v\n", result)
				}
			}
		}()

		wg.Wait()
	}

}

// Forward audio samples to GCP STT
func (t *Transcriber) forwardSpeech(buf []byte, speech speechpb.Speech_StreamingRecognizeClient) error {
	pkt, _, err := t.track.ReadRTP()
	if err != nil {
		return err
	}
	t.sampleBuilder.Push(pkt)

	for _, p := range t.sampleBuilder.PopPackets() {
		t.oggWriter.WriteRTP(p)

		n, err := t.oggReader.Read(buf)
		if err != nil {
			return err
		}

		if n > 0 {
			if err := speech.Send(&speechpb.StreamingRecognizeRequest{
				StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
					AudioContent: buf[:n],
				},
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

func newSpeechStream(ctx context.Context, speechClient *speech.Client, rtpCodec webrtc.RTPCodecParameters) (speechpb.Speech_StreamingRecognizeClient, error) {
	stream, err := speechClient.StreamingRecognize(ctx)
	if err != nil {
		return nil, err
	}

	// Send the initial configuration message.
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config: &speechpb.RecognitionConfig{
					Encoding:          speechpb.RecognitionConfig_OGG_OPUS,
					SampleRateHertz:   int32(rtpCodec.ClockRate),
					AudioChannelCount: int32(rtpCodec.Channels),
					LanguageCode:      "en-US", // TODO(theomonnom): Support multiple languages
				},
			},
		},
	}); err != nil {
		return nil, err
	}

	return stream, nil
}
