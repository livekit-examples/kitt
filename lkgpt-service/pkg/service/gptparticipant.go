package service

import (
	"context"
	"errors"
	"io"
	"strings"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/livekit/server-sdk-go/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"

	"github.com/pion/webrtc/v3/pkg/media/oggwriter"
)

var (
	ErrCodecNotSupported = errors.New("this codec isn't supported")
)

type GPTParticipant struct {
	room         *lksdk.Room
	speechClient *speech.Client
}

func ConnectGPTParticipant(url, token string, roomService *lksdk.RoomServiceClient) (*GPTParticipant, error) {
	roomCallback := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: p.trackSubscribed,
		},
	}

	// AutoSubscribe is enabled by default
	room, err := lksdk.ConnectToRoomWithToken(url, token, roomCallback)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	speechClient, err := speech.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	return &GPTParticipant{
		room:         room,
		speechClient: speechClient,
	}, nil
}

func (p *GPTParticipant) trackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	// Transcribe the audio track
	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	transcriber, err := NewTranscriber(track, p.speechClient)
	if err != nil {
		return
	}

	go transcriber.start()
}

func (p *GPTParticipant) Disconnect() {
	p.room.Disconnect()
	p.speechClient.Close()
}

type Transcriber struct {
	track         *webrtc.TrackRemote
	sampleBuilder *samplebuilder.SampleBuilder
	oggReader     *io.PipeReader
	oggWriter     *oggwriter.OggWriter
	speechClient  *speech.Client
}

func NewTranscriber(track *webrtc.TrackRemote, speechClient *speech.Client) (*Transcriber, error) {
	if !strings.EqualFold(track.Codec().MimeType, "audio/opus") {
		return nil, errors.New("only opus is supported")
	}

	rtpCodec := track.Codec()
	sb := samplebuilder.New(200, &codecs.OpusPacket{}, rtpCodec.ClockRate)

	oggReader, pw := io.Pipe()
	oggWriter, err := oggwriter.NewWith(pw, rtpCodec.ClockRate, rtpCodec.Channels)
	if err != nil {
		return nil, err
	}

	return &Transcriber{
		track:         track,
		sampleBuilder: sb,
		oggReader:     oggReader,
		oggWriter:     oggWriter,
		speechClient:  speechClient,
	}, nil
}

func (t *Transcriber) initSpeech(ctx context.Context) error {
	stream, err := t.speechClient.StreamingRecognize(ctx)
	if err != nil {
		return err
	}

	rtpCodec := t.track.Codec()

	// Send the initial configuration message.
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &speechpb.StreamingRecognitionConfig{
				Config: &speechpb.RecognitionConfig{
					Encoding:          speechpb.RecognitionConfig_OGG_OPUS,
					SampleRateHertz:   int32(rtpCodec.ClockRate),
					AudioChannelCount: int32(rtpCodec.Channels),
					LanguageCode:      "en-US", // TODO(theomonnom) Support multiple languages
				},
			},
		},
	}); err != nil {
		return err
	}

	return nil
}

func (t *Transcriber) start() {
	ctx := context.Background()
	buf := make([]byte, 1024)
	for {
		pkt, _, err := t.track.ReadRTP()
		if err != nil {
			return
		}
		t.sampleBuilder.Push(pkt)

		for _, p := range t.sampleBuilder.PopPackets() {
			t.oggWriter.WriteRTP(p)

			n, err := t.oggReader.Read(buf)
			if err != nil {
				return
			}

			if n > 0 {

			}
		}
	}
}
