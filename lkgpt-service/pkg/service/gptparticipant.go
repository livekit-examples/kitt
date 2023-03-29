package service

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	stt "cloud.google.com/go/speech/apiv1"
	sttpb "cloud.google.com/go/speech/apiv1/speechpb"
	tts "cloud.google.com/go/texttospeech/apiv1"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
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

	gptTrack *GPTTrack

	synthesizer *Synthesizer
	completion  *ChatCompletion
	isBusy      atomic.Bool

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

	track, err := NewGPTTrack()
	if err != nil {
		return nil, err
	}

	_, err = track.Publish(room.LocalParticipant)
	if err != nil {
		return nil, err
	}

	p.gptTrack = track
	p.room = room

	return p, nil
}

func (p *GPTParticipant) trackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if track.Kind() != webrtc.RTPCodecTypeAudio || publication.Source() != livekit.TrackSource_MICROPHONE {
		return
	}

	transcriber, err := NewTranscriber(track, p.sttClient, LanguageCode)
	if err != nil {
		logger.Errorw("failed to create the transcriber", err)
		return
	}

	logger.Infow("starting transcription for", "participant", rp.SID(), "track", track.ID())
	transcriber.OnTranscriptionReceived(p.onTranscriptionReceived(rp))
	go transcriber.Start()
}

func (p *GPTParticipant) onTranscriptionReceived(rp *lksdk.RemoteParticipant) func(resp *sttpb.StreamingRecognizeResponse) {
	return func(resp *sttpb.StreamingRecognizeResponse) {
		// Keep track of the conversation inside the room
		for _, result := range resp.Results {
			if result.IsFinal {
				prompt := result.Alternatives[0].Transcript

				go func() {
					// Answer the prompt if the GPTParticipant isn't already being "used"
					if p.isBusy.CompareAndSwap(false, true) {
						defer p.isBusy.Store(false)
						logger.Debugw("answering to", "participant", rp.SID(), "prompt", prompt)
						err := p.Answer(prompt)
						if err != nil && err != ErrBusy {
							logger.Errorw("failed to answer", err, "participant", rp.SID(), "prompt", prompt)
						}
					}
				}()

				sentence := &Sentence{
					Sid:        rp.SID(),
					Name:       rp.Name(),
					Transcript: prompt,
				}

				p.lock.Lock()
				p.conversation = append(p.conversation, sentence)
				p.lock.Unlock()
			}
		}
	}
}

func (p *GPTParticipant) Answer(prompt string) error {
	p.lock.Lock()
	tmp := make([]*Sentence, len(p.conversation))
	copy(tmp, p.conversation)
	p.lock.Unlock()

	stream, err := p.completion.Complete(context.Background(), tmp, prompt)
	if err != nil {
		logger.Errorw("failed to create openai completion stream", err)
		return err
	}

	// played is nil/closed if the last sentence has finished playing (used to guarantee the order of the sentences)
	var played chan struct{}
	var synthesizeWG sync.WaitGroup
	for {
		sentence, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			logger.Errorw("failed to receive completion stream", err)
			return err
		}

		logger.Debugw("synthesizing", "sentence", sentence)
		synthesizeWG.Add(1)
		tmpPlayed := played
		currentChan := make(chan struct{})
		go func() {
			defer close(currentChan)
			defer synthesizeWG.Done()

			_, err := p.synthesizer.Synthesize(context.Background(), sentence)
			if err != nil {
				logger.Errorw("failed to synthesize", err, "sentence", sentence)
				return
			}

			if tmpPlayed == nil {
				<-tmpPlayed
			}

			// Publish sample here

		}()

		played = currentChan
	}

	synthesizeWG.Wait()
	return nil
}

func (p *GPTParticipant) Disconnect() {
	p.room.Disconnect()
}
