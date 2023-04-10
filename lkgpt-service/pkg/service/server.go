package service

import (
	"context"
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

type ActiveParticipant struct {
	Connecting  bool
	Participant *GPTParticipant
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

	lock         sync.Mutex
	participants map[string]*ActiveParticipant
}

func NewLiveGPT(config *config.Config, sttClient *stt.Client, ttsClient *tts.Client) *LiveGPT {
	return &LiveGPT{
		config:       config,
		roomService:  lksdk.NewRoomServiceClient(config.LiveKit.Url, config.LiveKit.ApiKey, config.LiveKit.SecretKey),
		keyProvider:  auth.NewSimpleKeyProvider(config.LiveKit.ApiKey, config.LiveKit.SecretKey),
		doneChan:     make(chan struct{}),
		closedChan:   make(chan struct{}),
		participants: make(map[string]*ActiveParticipant),
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

		// If the GPT participant is not connected, connect it
		s.lock.Lock()
		if _, ok := s.participants[event.Room.Sid]; ok {
			s.lock.Unlock()
			logger.Infow("gpt participant already connected", "room", event.Room.Name, "participantCount", event.Room.NumParticipants)
			return
		}

		s.participants[event.Room.Sid] = &ActiveParticipant{
			Connecting: true,
		}
		s.lock.Unlock()

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

		logger.Infow("connecting gpt participant", "room", event.Room.Name)
		p, err := ConnectGPTParticipant(s.config.LiveKit.Url, jwt, s.sttClient, s.ttsClient, s.gptClient)
		if err != nil {
			logger.Errorw("error connecting gpt participant", err, "room", event.Room.Name)
			s.lock.Lock()
			delete(s.participants, event.Room.Sid)
			s.lock.Unlock()
			return
		}

		s.lock.Lock()
		s.participants[event.Room.Sid] = &ActiveParticipant{
			Connecting:  false,
			Participant: p,
		}
		s.lock.Unlock()

		p.OnDisconnected(func() {
			logger.Infow("gpt participant disconnected", "room", event.Room.Name)
			s.lock.Lock()
			delete(s.participants, event.Room.Sid)
			s.lock.Unlock()
		})
	}
}

func (s *LiveGPT) healthCheckHandler(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
