package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli"

	"github.com/livekit-examples/livegpt/pkg/config"
	"github.com/livekit-examples/livegpt/pkg/service"
)

func main() {
	app := cli.App{
		Name: "live-gpt",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "config",
				Usage: "path to the config file",
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

	var configContent string
	if configFile == "" {
		configContent = os.Getenv("LIVEGPT_CONFIG")
	} else {
		content, err := ioutil.ReadFile(configFile)
		if err != nil {
			return err
		}

		configContent = string(content)
	}

	conf, err := config.NewConfig(configContent)
	if err != nil {
		return err
	}

	server := service.NewLiveGPT(conf)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		sig := <-sigChan
		fmt.Printf("exit requested, shutting down: %v", sig)
		server.Stop()
	}()

	return server.Start()
}
