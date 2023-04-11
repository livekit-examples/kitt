# KITT

KITT is a ChatGPT-powered AI that lives in a WebRTC conference call. 

## Online Demo

You can try an online demo right now at <https://kitt.livekit.io/>

## Getting started

### Prerequisites

- `GOOGLE_APPLICATION_CREDENTIALS` json body from a Google cloud account. See <https://cloud.google.com/docs/authentication/application-default-credentials#GAC>
- OpenAI api key
- LiveKit api_key, api_secret, and url from [LiveKit Cloud](https://cloud.livekit.io)

### Run Instructions

Coming soon...

Currently locally running this project requires running the open source version of LiveKit because the design currently uses webhooks to create new Kitt participants. We're working on changing the design to remove the dependence on LiveKit's webhook feature.

## How it works

This repo contains two services:
1. meet
2. lkgpt-service

The `meet` service is a NexJS that implements a typical video call app. The `lkgpt-service` implements KITT. When a room is created, a webhook calls a handler in `lkgpt-service` which adds a participant to the room. The particpant uses GCP's speech-to-text, ChatGPT, and GCP's text-to-speech to create KITT.

The following diagram illustrates this:

![kitt-demo-architecture](https://user-images.githubusercontent.com/8453967/231060467-a2984951-71d9-45f4-ad5d-9eb35be229de.svg)



