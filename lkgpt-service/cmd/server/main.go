package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/urfave/cli"

	"github.com/livekit-examples/livegpt/pkg/config"
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

	return nil
}
