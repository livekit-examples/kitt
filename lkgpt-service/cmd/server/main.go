package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/livekit/protocol/logger"
	"github.com/urfave/cli/v2"
	"google.golang.org/api/option"

	"github.com/livekit-examples/livegpt/pkg/config"
	"github.com/livekit-examples/livegpt/pkg/service"

	stt "cloud.google.com/go/speech/apiv1"
	tts "cloud.google.com/go/texttospeech/apiv1"
)

func main() {
	app := cli.App{
		Name: "live-gpt",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Usage:   "LiveGPT yaml config file",
				EnvVars: []string{"LIVEGPT_CONFIG_FILE"},
			},
			&cli.StringFlag{
				Name:    "config-body",
				Usage:   "LiveGPT yaml config body",
				EnvVars: []string{"LIVEGPT_CONFIG_BODY"},
			},
			&cli.StringFlag{
				Name:    "gcp-credentials-path",
				Usage:   "Path to GCP credentials file",
				EnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS"},
			},
			&cli.StringFlag{
				Name:    "gcp-credentials-body",
				Usage:   "GCP credentials json body",
				EnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS_BODY"},
			},
		},
		Action: runServer,
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err)
	}
}

func runServer(c *cli.Context) error {
	configFile := c.String("config")
	configBody := c.String("config-body")
	if configBody == "" {
		if configFile == "" {
			return errors.New("config file or config body is required")
		}
		content, err := os.ReadFile(configFile)
		if err != nil {
			return err
		}
		configBody = string(content)
	}

	conf, err := config.NewConfig(configBody)
	if err != nil {
		return err
	}

	gcpFile := c.String("gcp-credentials-path")
	gcpBody := c.String("gcp-credentials-body")
	gcpCred := option.WithCredentialsFile(gcpFile)
	if gcpBody != "" {
		gcpCred = option.WithCredentialsJSON([]byte(gcpBody))
	}

	ctx := context.Background()
	sttClient, err := stt.NewClient(ctx, gcpCred)
	if err != nil {
		return err
	}

	ttsClient, err := tts.NewClient(ctx, gcpCred)
	if err != nil {
		return err
	}

	logger.InitFromConfig(conf.Logger, "livegpt")

	server := service.NewLiveGPT(conf, sttClient, ttsClient)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		logger.Infow("exit requested, shutting down", "signal", sig)
		server.Stop()
	}()

	return server.Start()
}
