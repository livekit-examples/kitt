package service

import (
	"fmt"
	"github.com/livekit-examples/livegpt/pkg/config"
	"github.com/urfave/negroni"
	"net/http"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"

	lksdk "github.com/livekit/server-sdk-go"
)

type LiveGPT struct {
	config      *config.Config
	httpServer  *http.Server
	roomService *lksdk.RoomServiceClient
	keyProvider *auth.SimpleKeyProvider
	doneChan    chan struct{}
	closedChan  chan struct{}
}

func NewLiveGPT(config *config.Config) *LiveGPT {
	return &LiveGPT{
		config: config,
	}
}

func (s *LiveGPT) Start() error {
	s.roomService = lksdk.NewRoomServiceClient(s.config.LiveKit.Host, s.config.LiveKit.ApiKey, s.config.LiveKit.SecretKey)
	s.keyProvider = auth.NewSimpleKeyProvider(s.config.LiveKit.ApiKey, s.config.LiveKit.SecretKey)

	n := negroni.New()
	n.Use(negroni.NewRecovery())
	n.Handler("/webhook", s.webhookHandler)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: n,
	}

	return nil
}

func (s *LiveGPT) Stop() error {
	return nil
}

func (s *LiveGPT) webhookHandler(w http.ResponseWriter, req *http.Request) {
	event, err := webhook.ReceiveWebhookEvent(req, s.keyProvider)
	if err != nil {
		fmt.Printf("error receiving webhook event: %w", err)
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
			fmt.Printf("error creating jwt: %w", err)
			return
		}

	}
}
