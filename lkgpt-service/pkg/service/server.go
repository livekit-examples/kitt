package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/livekit-examples/livegpt/pkg/config"
	"github.com/urfave/negroni"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/webhook"

	stt "cloud.google.com/go/speech/apiv1"
	tts "cloud.google.com/go/texttospeech/apiv1"
	lksdk "github.com/livekit/server-sdk-go"
	openai "github.com/sashabaranov/go-openai"
)

type ParticipantMetadata struct {
	LanguageCode string `json:"languageCode,omitempty"`
}

type LiveGPT struct {
	config      *config.Config
	roomService *lksdk.RoomServiceClient
	keyProvider *auth.SimpleKeyProvider
	gptClient   *openai.Client
	sttClient   *stt.Client
	ttsClient   *tts.Client

	httpServer *http.Server
	doneChan   chan struct{}
	closedChan chan struct{}

	participantsLock sync.Mutex
	participants     map[string]*GPTParticipant
}

func NewLiveGPT(config *config.Config, sttClient *stt.Client, ttsClient *tts.Client) *LiveGPT {
	return &LiveGPT{
		config:       config,
		roomService:  lksdk.NewRoomServiceClient(config.LiveKit.Url, config.LiveKit.ApiKey, config.LiveKit.SecretKey),
		keyProvider:  auth.NewSimpleKeyProvider(config.LiveKit.ApiKey, config.LiveKit.SecretKey),
		doneChan:     make(chan struct{}),
		closedChan:   make(chan struct{}),
		participants: make(map[string]*GPTParticipant),
		sttClient:    sttClient,
		ttsClient:    ttsClient,
	}
}

func (s *LiveGPT) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhook", s.webhookHandler)
	mux.HandleFunc("/", s.healthCheckHandler)

	n := negroni.New()
	n.Use(negroni.NewRecovery())
	n.UseHandler(mux)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: n,
	}

	openaiKey, ok := os.LookupEnv("OPENAI_API_KEY")
	if !ok {
		return errors.New("OPENAI_API_KEY environment variable is not set")
	}

	s.gptClient = openai.NewClient(openaiKey)

	httpListener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}

	go func() {
		logger.Infow("starting server", "port", s.config.Port)
		if err := s.httpServer.Serve(httpListener); err != http.ErrServerClosed {
			logger.Errorw("error starting server", err)
			s.Stop()
		}
	}()

	<-s.doneChan

	// Shutdown the server
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	_ = s.httpServer.Shutdown(ctx)

	s.sttClient.Close()
	s.ttsClient.Close()

	close(s.closedChan)
	return nil
}

func (s *LiveGPT) Stop() {
	close(s.doneChan)
	<-s.closedChan
}

func (s *LiveGPT) webhookHandler(w http.ResponseWriter, req *http.Request) {
	event, err := webhook.ReceiveWebhookEvent(req, s.keyProvider)
	if err != nil {
		logger.Errorw("error receiving webhook event", err)
		return
	}

	if event.Event == webhook.EventParticipantJoined {
		if event.Participant.Identity == BotIdentity {
			return
		}

		// TODO(theomonnom): Stateless?
		// If the GPT participant is not connected, connect it
		s.participantsLock.Lock()
		if _, ok := s.participants[event.Room.Sid]; ok {
			s.participantsLock.Unlock()
			logger.Infow("gpt participant already connected", "room", event.Room.Name)
			return
		}
		s.participantsLock.Unlock()

		metadata := ParticipantMetadata{}
		if event.Participant.Metadata != "" {
			err := json.Unmarshal([]byte(event.Participant.Metadata), &metadata)
			if err != nil {
				logger.Errorw("error unmarshalling participant metadata", err)
				return
			}
		}

		language, ok := Languages[metadata.LanguageCode]
		if !ok {
			language = DefaultLanguage
		}

		token := s.roomService.CreateToken().
			SetIdentity(BotIdentity).
			AddGrant(&auth.VideoGrant{
				Room:     event.Room.Name,
				RoomJoin: true,
			})

		jwt, err := token.ToJWT()
		if err != nil {
			logger.Errorw("error creating jwt", err)
			return
		}

		logger.Infow("connecting gpt participant", "room", event.Room.Name, "language", language.Code)
		p, err := ConnectGPTParticipant(s.config.LiveKit.Url, jwt, language, s.sttClient, s.ttsClient, s.gptClient)
		if err != nil {
			logger.Errorw("error connecting gpt participant", err, "room", event.Room.Name)
			return
		}

		s.participantsLock.Lock()
		s.participants[event.Room.Sid] = p
		s.participantsLock.Unlock()
	} else if event.Event == webhook.EventParticipantLeft {
		// If the GPT participant is alone, disconnect it
		s.participantsLock.Lock()
		defer s.participantsLock.Unlock()
		if p, ok := s.participants[event.Room.Sid]; ok {
			if event.Room.NumParticipants <= 1 {
				logger.Infow("disconnecting gpt participant", "room", event.Room.Name)
				p.Disconnect()
				delete(s.participants, event.Room.Sid)
			}
		}
	}
}

func (s *LiveGPT) healthCheckHandler(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
