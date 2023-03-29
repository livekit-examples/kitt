package main

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/livekit/protocol/logger"
	"github.com/urfave/cli/v2"

	"github.com/livekit-examples/livegpt/pkg/config"
	"github.com/livekit-examples/livegpt/pkg/service"
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

	logger.InitFromConfig(conf.Logger, "livegpt")

	server := service.NewLiveGPT(conf)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		logger.Infow("exit requested, shutting down", "signal", sig)
		server.Stop()
	}()

	return server.Start()
}
