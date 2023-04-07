package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	stt "cloud.google.com/go/speech/apiv1"
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

	BotIdentity = "kitt"
	BotName     = "KITT"

	Languages = map[string]*Language{
		"en-US": {
			Code:             "en-US",
			Label:            "English",
			SynthesizerModel: "en-US-News-N",
		},
		"fr-FR": {
			Code:             "fr-FR",
			Label:            "Français",
			SynthesizerModel: "fr-FR-Wavenet-B",
		},
	}
	DefaultLanguage = Languages["en-US"]
)

type Language struct {
	Code             string
	Label            string
	SynthesizerModel string
}

// A sentence in the conversation (Used for the history)
type Sentence struct {
	ParticipantName string
	IsBot           bool
	Text            string
}

type GPTParticipant struct {
	ctx    context.Context
	cancel context.CancelFunc

	language  *Language
	room      *lksdk.Room
	sttClient *stt.Client
	ttsClient *tts.Client
	gptClient *openai.Client

	gptTrack *GPTTrack

	transcribers map[string]*Transcriber
	synthesizer  *Synthesizer
	completion   *ChatCompletion
	isBusy       atomic.Bool

	lock         sync.Mutex
	conversation []*Sentence
}

func ConnectGPTParticipant(url, token string, language *Language, sttClient *stt.Client, ttsClient *tts.Client, gptClient *openai.Client) (*GPTParticipant, error) {
	ctx, cancel := context.WithCancel(context.Background())

	p := &GPTParticipant{
		ctx:          ctx,
		cancel:       cancel,
		sttClient:    sttClient,
		ttsClient:    ttsClient,
		gptClient:    gptClient,
		language:     language,
		transcribers: make(map[string]*Transcriber),
		synthesizer:  NewSynthesizer(ttsClient, language),
		completion:   NewChatCompletion(gptClient, language),
	}

	roomCallback := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackPublished:    p.trackPublished,
			OnTrackSubscribed:   p.trackSubscribed,
			OnTrackUnsubscribed: p.trackUnsubscribed,
		},
	}

	room, err := lksdk.ConnectToRoomWithToken(url, token, roomCallback, lksdk.WithAutoSubscribe(false))
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

func (p *GPTParticipant) trackPublished(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if publication.Source() != livekit.TrackSource_MICROPHONE || rp.Identity() == BotIdentity {
		return
	}

	err := publication.SetSubscribed(true)
	if err != nil {
		logger.Errorw("failed to subscribe to the track", err, "track", publication.SID(), "participant", rp.SID())
		return
	}
}

func (p *GPTParticipant) trackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if _, ok := p.transcribers[rp.SID()]; ok {
		return
	}

	transcriber, err := NewTranscriber(track, p.sttClient, p.language)
	if err != nil {
		logger.Errorw("failed to create the transcriber", err)
		return
	}

	p.transcribers[rp.SID()] = transcriber
	go func() {
		for result := range transcriber.Results() {
			p.onTranscriptionReceived(result, rp)
		}
	}()
}

func (p *GPTParticipant) trackUnsubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if transcriber, ok := p.transcribers[rp.SID()]; ok {
		transcriber.Close()
		delete(p.transcribers, rp.SID())
	}
}

func (p *GPTParticipant) onTranscriptionReceived(result RecognizeResult, rp *lksdk.RemoteParticipant) {
	if result.Error != nil {
		_ = p.sendErrorPacket(fmt.Sprintf("Sorry, an error occured while transcribing %s's speech using Google STT", rp.Identity()))
		return
	}

	_ = p.sendPacket(&packet{
		Type: packet_Transcript,
		Data: &transcriptPacket{
			Sid:        rp.SID(),
			Name:       rp.Name(),
			Transcript: result.Text,
			IsFinal:    result.IsFinal,
		},
	})

	if result.IsFinal {
		// Naive trigger implementation
		triggerBot := true
		if len(p.room.GetParticipants()) > 2 {
			triggerBot = false
			words := strings.Split(result.Text, " ")
			if len(words) >= 4 {
				triggerWords := strings.ToLower(strings.Join(words[:4], ""))
				if strings.Contains(triggerWords, "kit") || strings.Contains(triggerWords, "gpt") {
					triggerBot = true
				}
			}
		}

		prompt := &Sentence{
			ParticipantName: rp.Identity(),
			IsBot:           false,
			Text:            result.Text,
		}

		p.lock.Lock()
		// Don't include the current prompt in the history when answering
		history := make([]*Sentence, len(p.conversation))
		copy(history, p.conversation)

		p.conversation = append(p.conversation, prompt)
		p.lock.Unlock()

		if triggerBot && p.isBusy.CompareAndSwap(false, true) {
			go func() {
				defer p.isBusy.Store(false)

				_ = p.sendStatePacket(state_Loading)
				defer p.sendStatePacket(state_Idle)

				logger.Debugw("answering to", "participant", rp.SID(), "text", result.Text)
				answer, err := p.Answer(history, prompt) // Will send state_Speaking
				if err != nil {
					logger.Errorw("failed to answer", err, "participant", rp.SID(), "text", result.Text)
					return
				}

				botAnswer := &Sentence{
					ParticipantName: BotName,
					IsBot:           true,
					Text:            answer,
				}

				p.lock.Lock()
				p.conversation = append(p.conversation, botAnswer)
				p.lock.Unlock()
			}()
		}

	}
}

func (p *GPTParticipant) Answer(history []*Sentence, prompt *Sentence) (string, error) {
	stream, err := p.completion.Complete(p.ctx, history, prompt)
	if err != nil {
		return "", err
	}

	answerBuilder := strings.Builder{}

	var last chan struct{} // Used to order the goroutines (See QueueReader bellow)
	var wg sync.WaitGroup

	p.gptTrack.OnComplete(func(err error) {
		wg.Done()
	})

	for {
		sentence, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				break
			}

			_ = p.sendErrorPacket("Sorry, an error occured while communicating with OpenAI. It can happen when the servers are overloaded")
			return "", err
		}

		answerBuilder.WriteString(sentence)

		tmpLast := last
		currentChan := make(chan struct{})

		wg.Add(1)
		go func() {
			defer close(currentChan)
			defer wg.Done()

			logger.Debugw("synthesizing", "sentence", sentence)
			resp, err := p.synthesizer.Synthesize(p.ctx, sentence)
			if err != nil {
				logger.Errorw("failed to synthesize", err, "sentence", sentence)
				_ = p.sendErrorPacket("Sorry, an error occured while synthesizing voice data using Google TTS")
				return
			}

			if tmpLast != nil {
				<-tmpLast // Reorder outputs
			}

			logger.Debugw("finished synthesizing, queuing sentence", "sentence", sentence)
			err = p.gptTrack.QueueReader(bytes.NewReader(resp.AudioContent))
			if err != nil {
				logger.Errorw("failed to queue reader", err, "sentence", sentence)
				return
			}

			_ = p.sendStatePacket(state_Speaking)
			wg.Add(1)
		}()

		last = currentChan
	}

	wg.Wait()
	return answerBuilder.String(), nil
}

func (p *GPTParticipant) Disconnect() {
	p.room.Disconnect()

	for _, transcriber := range p.transcribers {
		transcriber.Close()
	}

	p.cancel()
}

// Packets sent over the datachannels
type packetType int32

const (
	packet_Transcript packetType = 0
	packet_State      packetType = 1
	packet_Error      packetType = 2 // Show an error message to the user screen
)

type gptState int32

const (
	state_Idle     gptState = 0
	state_Loading  gptState = 1
	state_Speaking gptState = 2
)

type packet struct {
	Type packetType  `json:"type"`
	Data interface{} `json:"data"`
}

type transcriptPacket struct {
	Sid        string `json:"sid"`
	Name       string `json:"name"`
	Transcript string `json:"transcript"`
	IsFinal    bool   `json:"isFinal"`
}

type statePacket struct {
	State gptState `json:"state"`
}

type errorPacket struct {
	Message string `json:"message"`
}

func (p *GPTParticipant) sendPacket(packet *packet) error {
	data, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	err = p.room.LocalParticipant.PublishData(data, livekit.DataPacket_RELIABLE, []string{})
	return err
}

func (p *GPTParticipant) sendStatePacket(state gptState) error {
	return p.sendPacket(&packet{
		Type: packet_State,
		Data: &statePacket{
			State: state,
		},
	})
}

func (p *GPTParticipant) sendErrorPacket(message string) error {
	return p.sendPacket(&packet{
		Type: packet_Error,
		Data: &errorPacket{
			Message: message,
		},
	})
}
