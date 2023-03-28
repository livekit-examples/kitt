package service

import (
	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
)

type Synthesizer struct {
	client *texttospeech.Client
}

func NewSynthesizer(client *texttospeech.Client) *Synthesizer {
	return &Synthesizer{
		client: client,
	}
}

func (s *Synthesizer) Synthesize(text string) error {

	res := texttospeechpb.SynthesizeSpeechRequest{}



	return nil
}
