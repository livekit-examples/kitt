package service

import (
	"errors"
	"strings"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/livekit/server-sdk-go/pkg/samplebuilder"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v3"
)

var (
	ErrCodecNotSupported = errors.New("this codec isn't supported")
)

type GPTParticipant struct {
	room *lksdk.Room
}

func ConnectGPTParticipant(url, token string, roomService *lksdk.RoomServiceClient) (*GPTParticipant, error) {
	roomCallback := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: trackSubscribed,
		},
	}

	// AutoSubscribe is enabled by default
	room, err := lksdk.ConnectToRoomWithToken(url, token, roomCallback)
	if err != nil {
		return nil, err
	}

	return &GPTParticipant{
		room: room,
	}, nil
}

func trackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	// Transcribe the audio track
	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	transcriber, err := NewTranscriber(track)
	if err != nil {
		return
	}

	go transcriber.Start()
}

type Transcriber struct {
	track *webrtc.TrackRemote
	sb    *samplebuilder.SampleBuilder
}

func NewTranscriber(track *webrtc.TrackRemote) (*Transcriber, error) {
	if !strings.EqualFold(track.Codec().MimeType, "audio/opus") {
		return nil, errors.New("only opus is supported")
	}

	sb := samplebuilder.New(200, &codecs.OpusPacket{}, track.Codec().ClockRate)
	return &Transcriber{
		track: track,
		sb:    sb,
	}, nil
}

func (t *Transcriber) Start() {
	for {
		pkt, _, err := t.track.ReadRTP()
		if err != nil {
			break
		}
		t.sb.Push(pkt)

		for _, p := range t.sb.PopPackets() {

		}
	}
}
