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
	"github.com/livekit/protocol/webhook"

	speech "cloud.google.com/go/speech/apiv1"
	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	lksdk "github.com/livekit/server-sdk-go"
	openai "github.com/sashabaranov/go-openai"
)

type LiveGPT struct {
	config      *config.Config
	roomService *lksdk.RoomServiceClient
	keyProvider *auth.SimpleKeyProvider
	gptClient   *openai.Client
	sttClient   *speech.Client
	ttsClient   *texttospeech.Client

	httpServer *http.Server
	doneChan   chan struct{}
	closedChan chan struct{}

	participantsLock sync.Mutex
	participants     map[string]*GPTParticipant
}

func NewLiveGPT(config *config.Config) *LiveGPT {
	return &LiveGPT{
		config:       config,
		roomService:  lksdk.NewRoomServiceClient(config.LiveKit.Url, config.LiveKit.ApiKey, config.LiveKit.SecretKey),
		keyProvider:  auth.NewSimpleKeyProvider(config.LiveKit.ApiKey, config.LiveKit.SecretKey),
		doneChan:     make(chan struct{}),
		closedChan:   make(chan struct{}),
		participants: make(map[string]*GPTParticipant),
	}
}

func (s *LiveGPT) Start() error {
	n := negroni.New()
	n.Use(negroni.NewRecovery())
	n.UseHandlerFunc(s.webhookHandler)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: n,
	}

	ctx := context.Background()
	sttClient, err := speech.NewClient(ctx)
	if err != nil {
		return err
	}

	ttsClient, err := texttospeech.NewClient(ctx)
	if err != nil {
		return err
	}

	openaiKey, ok := os.LookupEnv("OPENAI_API_KEY")
	if !ok {
		return errors.New("OPENAI_API_KEY environment variable is not set")
	}

	s.sttClient = sttClient
	s.ttsClient = ttsClient
	s.gptClient = openai.NewClient(openaiKey)

	httpListener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}

	go func() {
		fmt.Printf("starting LiveGPT server on port %d", s.config.Port)
		if err := s.httpServer.Serve(httpListener); err != http.ErrServerClosed {
			fmt.Printf("error starting server: %v", err)
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
		fmt.Printf("error receiving webhook event: %v", err)
		return
	}

	if event.Event == webhook.EventRoomStarted {
		token := s.roomService.CreateToken()

		grant := &auth.VideoGrant{
			Room:     event.Room.Name,
			RoomJoin: true,
		}

		token.SetIdentity("livegpt").
			AddGrant(grant)

		jwt, err := token.ToJWT()
		if err != nil {
			fmt.Printf("error creating jwt: %v", err)
			return
		}

		fmt.Printf("connecting gpt participant to %v", s.config.LiveKit.Url)
		p, err := ConnectGPTParticipant(s.config.LiveKit.Url, jwt, s.sttClient, s.ttsClient, s.gptClient)
		if err != nil {
			fmt.Printf("error connecting gpt participant: %v", err)
			return
		}

		s.participantsLock.Lock()
		s.participants[event.Room.Name] = p
		s.participantsLock.Unlock()
	} else if event.Event == webhook.EventParticipantLeft {
		// If the GPT participant is alone, disconnect it

	}
}
