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
	"time"

	stt "cloud.google.com/go/speech/apiv1"
	tts "cloud.google.com/go/texttospeech/apiv1"
	"github.com/pion/webrtc/v3"
	"github.com/sashabaranov/go-openai"
	"golang.org/x/exp/slices"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"
)

var (
	ErrCodecNotSupported = errors.New("this codec isn't supported")
	ErrBusy              = errors.New("the gpt participant is already used")

	BotIdentity = "KITT"

	// Naive trigger/activation implementation
	GreetingWords = []string{"hi", "hello", "hey", "hallo", "salut", "bonjour", "hola", "eh", "ey"}
	NameWords     = []string{"kit", "gpt", "kitt", "livekit", "live-kit", "kid"}

	ActivationWordsLen = 2
	ActivationTimeout  = 4 * time.Second // If the participant didn't say anything for this duration, stop listening

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

type ParticipantMetadata struct {
	LanguageCode string `json:"languageCode,omitempty"`
}

type GPTParticipant struct {
	ctx    context.Context
	cancel context.CancelFunc

	room      *lksdk.Room
	sttClient *stt.Client
	ttsClient *tts.Client
	gptClient *openai.Client

	gptTrack *GPTTrack

	transcribers map[string]*Transcriber
	synthesizer  *Synthesizer
	completion   *ChatCompletion

	lock           sync.Mutex
	onDisconnected func()
	events         []*MeetingEvent

	// Current active participant
	isBusy            atomic.Bool
	activeInterim     atomic.Bool // True when KITT has been activated using an interim result
	activeId          uint64
	activeParticipant *lksdk.RemoteParticipant // If set, answer his next sentence/question
	lastActivity      time.Time
}

func ConnectGPTParticipant(url, token string, sttClient *stt.Client, ttsClient *tts.Client, gptClient *openai.Client) (*GPTParticipant, error) {
	ctx, cancel := context.WithCancel(context.Background())

	p := &GPTParticipant{
		ctx:          ctx,
		cancel:       cancel,
		sttClient:    sttClient,
		ttsClient:    ttsClient,
		gptClient:    gptClient,
		transcribers: make(map[string]*Transcriber),
		synthesizer:  NewSynthesizer(ttsClient),
		completion:   NewChatCompletion(gptClient),
	}

	roomCallback := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackPublished:    p.trackPublished,
			OnTrackSubscribed:   p.trackSubscribed,
			OnTrackUnsubscribed: p.trackUnsubscribed,
		},
		OnParticipantDisconnected: p.participantDisconnected,
		OnDisconnected:            p.disconnected,
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

	go func() {
		// Check if there's no participant when KITT joins.
		// It can happen when the participant who created the room directly leaves.
		time.Sleep(5 * time.Second)
		if len(room.GetParticipants()) == 0 {
			p.Disconnect()
		}
	}()

	return p, nil
}

func (p *GPTParticipant) OnDisconnected(f func()) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.onDisconnected = f
}

func (p *GPTParticipant) Disconnect() {
	logger.Infow("disconnecting gpt participant", "room", p.room.Name())
	p.room.Disconnect()

	for _, transcriber := range p.transcribers {
		transcriber.Close()
	}

	p.cancel()

	p.lock.Lock()
	onDisconnected := p.onDisconnected
	p.lock.Unlock()

	if onDisconnected != nil {
		onDisconnected()
	}
}

func (p *GPTParticipant) trackPublished(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if publication.Source() != livekit.TrackSource_MICROPHONE {
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

	metadata := ParticipantMetadata{}
	if rp.Metadata() != "" {
		err := json.Unmarshal([]byte(rp.Metadata()), &metadata)
		if err != nil {
			logger.Warnw("error unmarshalling participant metadata", err)
		}
	}

	language, ok := Languages[metadata.LanguageCode]
	if !ok {
		language = DefaultLanguage
	}

	logger.Infow("starting to transcribe", "participant", rp.Identity(), "language", language.Code)
	transcriber, err := NewTranscriber(track.Codec(), p.sttClient, language)
	if err != nil {
		logger.Errorw("failed to create the transcriber", err)
		return
	}

	p.transcribers[rp.SID()] = transcriber
	go func() {
		for result := range transcriber.Results() {
			p.onTranscriptionReceived(result, rp, transcriber)
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
	if transcriber, ok := p.transcribers[rp.SID()]; ok {
		p.lock.Unlock()
		transcriber.Close()
		p.lock.Lock()
		delete(p.transcribers, rp.SID())
	}
	p.lock.Unlock()
}

func (p *GPTParticipant) participantDisconnected(rp *lksdk.RemoteParticipant) {
	participants := p.room.GetParticipants()
	logger.Debugw("participant disconnected", "numParticipants", len(participants))
	if len(participants) == 0 {
		p.Disconnect()
	}
}

func (p *GPTParticipant) disconnected() {
	p.Disconnect()
}

// In a multi-user meeting, the bot will only answer when it is activated.
// Activate the participant rp
func (p *GPTParticipant) activateParticipant(rp *lksdk.RemoteParticipant) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.activeParticipant != rp {
		p.activeId++
		p.activeParticipant = rp
		p.lastActivity = time.Now()
		_ = p.sendStatePacket(state_Active)

		tmpActiveId := p.activeId
		go func() {
			time.Sleep(ActivationTimeout)
			for {
				p.lock.Lock()
				if p.activeId != tmpActiveId {
					p.lock.Unlock()
					return
				}

				if time.Since(p.lastActivity) >= ActivationTimeout {
					p.activeParticipant = nil
					_ = p.sendStatePacket(state_Idle)
					p.lock.Unlock()
					return
				}

				p.lock.Unlock()
				time.Sleep(1 * time.Second)
			}
		}()
	}
}

func (p *GPTParticipant) onTranscriptionReceived(result RecognizeResult, rp *lksdk.RemoteParticipant, transcriber *Transcriber) {
	if result.Error != nil {
		_ = p.sendErrorPacket(fmt.Sprintf("Sorry, an error occured while transcribing %s's speech using Google STT", rp.Identity()))
		return
	}

	_ = p.sendPacket(&packet{
		Type: packet_Transcript,
		Data: &transcriptPacket{
			Sid:     rp.SID(),
			Name:    rp.Name(),
			Text:    result.Text,
			IsFinal: result.IsFinal,
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
	if activeParticipant == rp {
		p.lastActivity = time.Now()
	}
	p.lock.Unlock()

	shouldAnswer := false
	if len(p.room.GetParticipants()) == 1 {
		// Always answer when we're alone with KITT
		if activeParticipant == nil {
			activeParticipant = rp
			p.activateParticipant(rp)
		}

		shouldAnswer = result.IsFinal
	} else {
		// Check if the participant is activating the KITT
		justActivated := false
		words := strings.Split(strings.ToLower(strings.TrimSpace(result.Text)), " ")
		if len(words) >= 2 { // No max length but only check the first 3 words
			limit := len(words)
			if limit > ActivationWordsLen {
				limit = ActivationWordsLen
			}
			activationWords := words[:limit]

			// Check if text contains at least one GreentingWords
			greetIndex := -1
			for _, greet := range GreetingWords {
				if greetIndex = slices.Index(activationWords, greet); greetIndex != -1 {
					break
				}
			}

			nameIndex := -1
			for _, name := range NameWords {
				if nameIndex = slices.Index(activationWords, name); nameIndex != -1 {
					break
				}
			}

			if greetIndex < nameIndex && greetIndex != -1 {
				justActivated = true
				p.activeInterim.Store(!result.IsFinal)
				if activeParticipant != rp {
					activeParticipant = rp
					logger.Debugw("activating KITT for participant", "activationText", strings.Join(activationWords, " "), "participant", rp.Identity())
					p.activateParticipant(rp)
				}
			}
		}

		if result.IsFinal {
			shouldAnswer = activeParticipant == rp
			if (justActivated || p.activeInterim.Load()) && len(words) <= ActivationWordsLen+1 {
				// Ignore if the participant stopped speaking after the activation, answer his next sentence
				shouldAnswer = false
			}
		}
	}

	if shouldAnswer {
		prompt := &SpeechEvent{
			ParticipantName: rp.Identity(),
			IsBot:           false,
			Text:            result.Text,
		}

		p.lock.Lock()

		// Don't include the current prompt in the history when answering
		events := make([]*MeetingEvent, len(p.events))
		copy(events, p.events)
		p.events = append(p.events, &MeetingEvent{
			Speech: prompt,
		})
		p.activeParticipant = nil
		p.lock.Unlock()

		if shouldAnswer && p.isBusy.CompareAndSwap(false, true) {
			go func() {
				defer p.isBusy.Store(false)
				_ = p.sendStatePacket(state_Loading)

				logger.Debugw("answering to", "participant", rp.SID(), "text", result.Text)
				answer, err := p.answer(events, prompt, rp, transcriber.Language()) // Will send state_Speaking
				if err != nil {
					logger.Errorw("failed to answer", err, "participant", rp.SID(), "text", result.Text)
					p.sendStatePacket(state_Idle)
					return
				}

				// KITT finished speaking, check if the last sentence was a question.
				// If so, auto activate the current participant
				if strings.HasSuffix(answer, "?") {
					// Checking this suffix should be enough
					p.activateParticipant(rp)
				} else {
					p.sendStatePacket(state_Idle)
				}

				botAnswer := &SpeechEvent{
					ParticipantName: BotIdentity,
					IsBot:           true,
					Text:            answer,
				}

				p.lock.Lock()
				p.events = append(p.events, &MeetingEvent{
					Speech: botAnswer,
				})
				p.lock.Unlock()
			}()
		}
	}
}

func (p *GPTParticipant) answer(events []*MeetingEvent, prompt *SpeechEvent, rp *lksdk.RemoteParticipant, language *Language) (string, error) {
	stream, err := p.completion.Complete(p.ctx, events, prompt, rp, p.room, language)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", nil
		}

		_ = p.sendErrorPacket("Sorry, an error occured while communicating with OpenAI. Max context length reached?")
		return "", err
	}

	var last chan struct{} // Used to order the goroutines (See QueueReader bellow)
	var wg sync.WaitGroup

	p.gptTrack.OnComplete(func(err error) {
		wg.Done()
	})

	sb := strings.Builder{}
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
		trimSentence := strings.TrimSpace(sentence)
		lowerSentence := strings.ToLower(trimSentence)
		for code, lang := range Languages {
			prefix1 := strings.ToLower(fmt.Sprintf("<%s>", code))
			prefix2 := strings.ToLower(code)

			if strings.HasPrefix(lowerSentence, prefix1) {
				trimSentence = trimSentence[len(prefix1):]
			} else if strings.HasPrefix(lowerSentence, prefix2) {
				trimSentence = trimSentence[len(prefix2):]
			} else {
				continue
			}

			language = lang
			break
		}

		sb.WriteString(trimSentence)
		sb.WriteString(" ")

		tmpLast := last
		tmpLang := language
		currentCh := make(chan struct{})

		wg.Add(1)
		go func() {
			defer close(currentCh)
			defer wg.Done()

			logger.Debugw("synthesizing", "sentence", trimSentence)
			resp, err := p.synthesizer.Synthesize(p.ctx, trimSentence, tmpLang)
			if err != nil {
				logger.Errorw("failed to synthesize", err, "sentence", trimSentence)
				_ = p.sendErrorPacket("Sorry, an error occured while synthesizing voice data using Google TTS")
				return
			}

			if tmpLast != nil {
				<-tmpLast // Reorder outputs
			}

			logger.Debugw("finished synthesizing, queuing sentence", "sentence", trimSentence)
			err = p.gptTrack.QueueReader(bytes.NewReader(resp.AudioContent))
			if err != nil {
				logger.Errorw("failed to queue reader", err, "sentence", trimSentence)
				return
			}

			_ = p.sendStatePacket(state_Speaking)
			wg.Add(1)
		}()

		last = currentCh
	}

	wg.Wait()

	return strings.TrimSpace(sb.String()), nil
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
	Sid     string `json:"sid"`
	Name    string `json:"name"`
	Text    string `json:"text"`
	IsFinal bool   `json:"isFinal"`
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
	return p.room.LocalParticipant.PublishData(data, livekit.DataPacket_RELIABLE, []string{})
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
