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

	// Naive trigger implementation
	GreetingWords = []string{"hi", "hello", "hey", "hallo", "salut", "bonjour", "hola", "eh", "ey", "嘿", "你好", "やあ", "おい"}

	Languages = map[string]*Language{
		"en-US": {
			Code:             "en-US",
			Label:            "English",
			TranscriberCode:  "en-US",
			SynthesizerModel: "en-US-Wavenet-D",
		},
		"fr-FR": {
			Code:             "fr-FR",
			Label:            "Français",
			TranscriberCode:  "fr-FR",
			SynthesizerModel: "fr-FR-Wavenet-B",
		},
		"de-DE": {
			Code:             "de-DE",
			Label:            "German",
			TranscriberCode:  "de-DE",
			SynthesizerModel: "de-DE-Wavenet-B",
		},
		"ja-JP": {
			Code:             "ja-JP",
			Label:            "Japanese",
			TranscriberCode:  "ja-JP",
			SynthesizerModel: "ja-JP-Wavenet-D",
		},
		"cmn-CN": {
			Code:             "cmn-CN",
			Label:            "Mandarin Chinese",
			TranscriberCode:  "zh",
			SynthesizerModel: "cmn-CN-Wavenet-C",
		},
		"es-ES": {
			Code:             "es-ES",
			Label:            "Spanish",
			TranscriberCode:  "es-ES",
			SynthesizerModel: "es-ES-Wavenet-B",
		},
	}
	DefaultLanguage = Languages["en-US"]
)

type Language struct {
	Code             string
	Label            string
	TranscriberCode  string
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

	isBusy atomic.Bool

	lock              sync.Mutex
	conversation      []*Sentence
	activeParticipant *lksdk.RemoteParticipant // If set, answer his next sentence/question
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
		synthesizer:  NewSynthesizer(ttsClient),
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

func (p *GPTParticipant) Disconnect() {
	p.room.Disconnect()

	for _, transcriber := range p.transcribers {
		transcriber.Close()
	}

	p.cancel()
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

	transcriber, err := NewTranscriber(track.Codec(), p.sttClient, p.language)
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

	// Forward track packets to the transcriber
	go func() {
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				if err != io.EOF {
					logger.Errorw("failed to read track", err, "participant", rp.SID())
				}
				return
			}

			err = transcriber.WriteRTP(pkt)
			if err != nil {
				if err != io.EOF {
					logger.Errorw("failed to forward pkt to the transcriber", err, "participant", rp.SID())
				}
				return
			}
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

	// When there's only one participant in the meeting, no activation/trigger is needed
	// The bot will answer directly.
	//
	// When there are multiple participants, activation is required.
	// 1. Wait for activation sentence (Hey Kitt!)
	// 2. If the participant stop speaking after the activation, ignore the next "isFinal" result
	// 3. If activated, anwser the next sentence

	p.lock.Lock()
	activeParticipant := p.activeParticipant
	p.lock.Unlock()

	shouldAnswer := false
	if len(p.room.GetParticipants()) == 2 {
		// Always answer when we're alone with KITT
		if activeParticipant == nil {
			// Still activate it to play the right animations clientside
			activeParticipant = rp
			p.lock.Lock()
			p.activeParticipant = activeParticipant
			p.lock.Unlock()
			_ = p.sendStatePacket(state_Active)
		}

		shouldAnswer = result.IsFinal
	} else {
		// Check if the participant is activating the KITT
		justActivated := false
		words := strings.Split(strings.TrimSpace(result.Text), " ")
		if len(words) >= 2 { // No max length but only check the first 4 words
			limit := len(words)
			if limit > 4 {
				limit = 4
			}
			text := strings.ToLower(strings.Join(words[:limit], ""))

			// Check if text contains at least one GreentingWords
			greeting := false
			for _, greet := range GreetingWords {
				if strings.Contains(text, greet) {
					greeting = true
					break
				}
			}
			subject := strings.Contains(text, "kit") || strings.Contains(text, "gpt")
			if greeting && subject {
				justActivated = true
				if activeParticipant != rp {
					activeParticipant = rp

					p.lock.Lock()
					p.activeParticipant = rp
					p.lock.Unlock()

					logger.Debugw("activating KITT for participant", "participant", rp.Identity())
					_ = p.sendStatePacket(state_Active)
				}

			}
		}

		if result.IsFinal {
			shouldAnswer = activeParticipant == rp
			if justActivated && len(words) <= 4 {
				// Ignore if the participant stopped speaking after the activation
				// Answer his next sentence
				shouldAnswer = false
			}
		}
	}

	if shouldAnswer {
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

		p.activeParticipant = nil
		p.lock.Unlock()

		if shouldAnswer && p.isBusy.CompareAndSwap(false, true) {
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

	var last chan struct{} // Used to order the goroutines (See QueueReader bellow)
	var wg sync.WaitGroup

	p.gptTrack.OnComplete(func(err error) {
		wg.Done()
	})

	sb := strings.Builder{}
	language := p.language // Used language for the current sentence
	for {
		sentence, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				break
			}

			_ = p.sendErrorPacket("Sorry, an error occured while communicating with OpenAI. It can happen when the servers are overloaded")
			return "", err
		}

		// Try to parse the language from the sentence (ChatGPT can provide <en-US>, en-US as a prefix)
		trimSentence := strings.TrimSpace(strings.ToLower(sentence))
		for code, lang := range Languages {
			prefix1 := strings.ToLower(fmt.Sprintf("<%s>", code))
			prefix2 := strings.ToLower(code)

			if strings.HasPrefix(trimSentence, prefix1) {
				sentence = sentence[len(prefix1):]
			} else if strings.HasPrefix(trimSentence, prefix2) {
				sentence = sentence[len(prefix2):]
			} else {
				continue
			}

			language = lang
		}

		sb.WriteString(sentence)
		tmpLast := last
		tmpLang := language
		currentCh := make(chan struct{})

		wg.Add(1)
		go func() {
			defer close(currentCh)
			defer wg.Done()

			logger.Debugw("synthesizing", "sentence", sentence)
			resp, err := p.synthesizer.Synthesize(p.ctx, sentence, tmpLang)
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

		last = currentCh
	}

	wg.Wait()
	return sb.String(), nil
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
	state_Active   gptState = 3
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
