package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	stt "cloud.google.com/go/speech/apiv1"
	sttpb "cloud.google.com/go/speech/apiv1/speechpb"
	tts "cloud.google.com/go/texttospeech/apiv1"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/pion/webrtc/v3"
	"github.com/sashabaranov/go-openai"
)

var (
	ErrCodecNotSupported = errors.New("this codec isn't supported")
	ErrBusy              = errors.New("the gpt participant is already used")
)

const LanguageCode = "en-US"

type Sentence struct {
	Sid        string // participant sid
	Name       string
	Transcript string
}

type GPTParticipant struct {
	room      *lksdk.Room
	sttClient *stt.Client
	ttsClient *tts.Client
	gptClient *openai.Client

	synthesizer *Synthesizer
	completion  *ChatCompletion
	isBusy      atomic.Bool

	trackWriter io.WriteCloser

	lock         sync.Mutex
	conversation []*Sentence
}

func ConnectGPTParticipant(url, token string, sttClient *stt.Client, ttsClient *tts.Client, gptClient *openai.Client) (*GPTParticipant, error) {
	p := &GPTParticipant{
		sttClient:   sttClient,
		ttsClient:   ttsClient,
		gptClient:   gptClient,
		synthesizer: NewSynthesizer(ttsClient, LanguageCode),
		completion:  NewChatCompletion(gptClient, LanguageCode),
	}
	roomCallback := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: p.trackSubscribed,
		},
	}

	room, err := lksdk.ConnectToRoomWithToken(url, token, roomCallback) // AutoSubscribe is enabled by default
	if err != nil {
		return nil, err
	}

	rp, wp := io.Pipe()
	track, err := lksdk.NewLocalReaderTrack(rp, webrtc.MimeTypeOpus) // Synthesizer is configured to give us OGG_OPUS
	if err != nil {
		return nil, err
	}

	_, err = room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{})
	if err != nil {
		return nil, err
	}

	p.room = room
	p.trackWriter = wp

	return p, nil
}

func (p *GPTParticipant) trackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	transcriber, err := NewTranscriber(track, p.sttClient, LanguageCode)
	if err != nil {
		fmt.Printf("failed to create the transcriber: %v", err)
		return
	}

	fmt.Printf("starting transcription for %s", publication.SID())
	transcriber.OnTranscriptionReceived(p.onTranscriptionReceived(rp))
	go transcriber.Start()
}

func (p *GPTParticipant) onTranscriptionReceived(rp *lksdk.RemoteParticipant) func(resp *sttpb.StreamingRecognizeResponse) {
	return func(resp *sttpb.StreamingRecognizeResponse) {
		// Keep track of the conversation inside the room
		for _, result := range resp.Results {
			if result.IsFinal {
				transcript := result.Alternatives[0].Transcript

				go func() {
					fmt.Printf("answering to %s: %s", rp.SID(), transcript)
					err := p.Answer(transcript)
					if err != nil && err != ErrBusy {
						fmt.Printf("failed to answer: %v", err)
					}
				}()

				sentence := &Sentence{
					Sid:        rp.SID(),
					Name:       rp.Name(),
					Transcript: transcript,
				}

				p.lock.Lock()
				p.conversation = append(p.conversation, sentence)
				p.lock.Unlock()
			}
		}
	}
}

func (p *GPTParticipant) Answer(prompt string) error {
	if !p.isBusy.CompareAndSwap(false, true) {
		return ErrBusy
	}

	defer p.isBusy.Store(false)

	p.lock.Lock()
	tmp := make([]*Sentence, len(p.conversation))
	copy(tmp, p.conversation)
	p.lock.Unlock()

	stream, err := p.completion.Complete(context.Background(), tmp, prompt)
	if err != nil {
		fmt.Printf("failed to create completion stream %v", err)
		return err
	}

	// nil/closed if the last sentence has finished playing (used to guarantee the order of the sentences)
	var played chan struct{}
	var synthesizeWG sync.WaitGroup
	for {
		sentence, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			fmt.Printf("failed to receive completion stream %v", err)
			return err
		}

		fmt.Printf("synthesizing %s", sentence)

		synthesizeWG.Add(1)
		tmpPlayed := played
		currentChan := make(chan struct{})
		go func() {
			defer synthesizeWG.Done()
			defer close(currentChan)

			resp, err := p.synthesizer.Synthesize(context.Background(), sentence)
			if err != nil {
				fmt.Printf("failed to synthesize %v", err)
				return
			}

			if tmpPlayed == nil {
				<-tmpPlayed
			}

			_, err = p.trackWriter.Write(resp.AudioContent)
			if err != nil {
				fmt.Printf("failed to write to the track %v", err)
				return
			}

		}()

		played = currentChan
	}

	synthesizeWG.Wait()
	return nil
}

func (p *GPTParticipant) Disconnect() {
	p.room.Disconnect()
}
