package service

import (
	"context"
	"errors"
	"fmt"

	speech "cloud.google.com/go/speech/apiv1"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/webrtc/v3"
)

var (
	ErrCodecNotSupported = errors.New("this codec isn't supported")
)

type GPTParticipant struct {
	room         *lksdk.Room
	speechClient *speech.Client
}

func ConnectGPTParticipant(url, token string) (*GPTParticipant, error) {
	p := &GPTParticipant{}
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

	p.room = room
	p.speechClient = speechClient

	return p, nil
}

func (p *GPTParticipant) trackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	// transcribe the audio track
	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	transcriber, err := NewTranscriber(track, p.speechClient)
	if err != nil {
		fmt.Printf("error creating transcriber: %v", err)
		return
	}

	fmt.Printf("Starting transcription for %s", publication.SID())
	go transcriber.Start()
}

func (p *GPTParticipant) Disconnect() {
	p.room.Disconnect()
	p.speechClient.Close()
}
