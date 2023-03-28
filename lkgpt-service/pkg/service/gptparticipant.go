package service

import (
	"errors"
	"fmt"
	"sync"

	speech "cloud.google.com/go/speech/apiv1"
	"cloud.google.com/go/speech/apiv1/speechpb"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/webrtc/v3"
	"github.com/sashabaranov/go-openai"
)

var (
	ErrCodecNotSupported = errors.New("this codec isn't supported")
)

type Sentence struct {
	Sid        string // participant sid
	Name       string
	Transcript string
}

type GPTParticipant struct {
	room         *lksdk.Room
	speechClient *speech.Client
	completion   *ChatCompletion

	lock         sync.Mutex
	conversation []*Sentence
}

func ConnectGPTParticipant(url, token string, speechClient *speech.Client, openaiClient *openai.Client) (*GPTParticipant, error) {
	p := &GPTParticipant{
		speechClient: speechClient,
		completion:   NewChatCompletion(openaiClient),
	}
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

	p.room = room
	return p, nil
}

func (p *GPTParticipant) trackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	// transcribe the audio track
	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	transcriber, err := NewTranscriber(track, p.speechClient, "en-US")
	if err != nil {
		fmt.Printf("error creating transcriber: %v", err)
		return
	}

	transcriber.OnTranscriptionReceived(func(resp *speechpb.StreamingRecognizeResponse) {
		// Keep track of the conversation inside the room
		for _, result := range resp.Results {
			if result.IsFinal {
				sentence := &Sentence{
					Sid:        rp.SID(),
					Name:       rp.Name(),
					Transcript: result.Alternatives[0].Transcript,
				}

				p.lock.Lock()
				p.conversation = append(p.conversation, sentence)
				p.lock.Unlock()
			}
		}
	})

	fmt.Printf("Starting transcription for %s", publication.SID())
	go transcriber.Start()
}

func (p *GPTParticipant) Disconnect() {
	p.room.Disconnect()
	p.speechClient.Close()
}
