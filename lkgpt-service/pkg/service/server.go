package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/livekit-examples/livegpt/pkg/config"
	"github.com/urfave/negroni"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/webhook"

	lksdk "github.com/livekit/server-sdk-go"
)

type LiveGPT struct {
	config      *config.Config
	roomService *lksdk.RoomServiceClient
	keyProvider *auth.SimpleKeyProvider

	httpServer *http.Server
	doneChan   chan struct{}
	closedChan chan struct{}

	participantsLock sync.Mutex
	participants     map[string]*GPTParticipant
}

func NewLiveGPT(config *config.Config) *LiveGPT {
	return &LiveGPT{
		config:       config,
		roomService:  lksdk.NewRoomServiceClient(config.LiveKit.Host, config.LiveKit.ApiKey, config.LiveKit.SecretKey),
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

	httpListener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}

	go func() {
		fmt.Printf("starting LiveGPT server")
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

		p, err := ConnectGPTParticipant(s.config.LiveKit.Host, jwt)
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
