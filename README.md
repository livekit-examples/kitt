# KITT

KITT is a ChatGPT-powered AI that lives in a WebRTC conference call.

<img src="https://user-images.githubusercontent.com/8453967/231227021-4f5a4412-ff14-4837-97e7-c55a1d9717c4.gif" 
        alt="Preview gif" 
        width="640" 
        />

## Online Demo

You can try an online demo right now at <https://livekit.io/kitt>

## How it works

This repo contains two services:
1. meet
2. lkgpt-service

The `meet` service is a NextJS app that implements a typical video call app. The `lkgpt-service` implements KITT. When a room is created, a webhook calls a handler in `lkgpt-service` which adds a participant to the room. The particpant uses GCP's speech-to-text, ChatGPT, and GCP's text-to-speech to create KITT.

The following diagram illustrates this:

![kitt-demo-architecture](https://user-images.githubusercontent.com/8453967/231060467-a2984951-71d9-45f4-ad5d-9eb35be229de.svg)

## Getting started

### Prerequisites

- `GOOGLE_APPLICATION_CREDENTIALS` json body from a Google cloud account. See <https://cloud.google.com/docs/authentication/application-default-credentials#GAC>
- OpenAI api key
- LiveKit api_key, api_secret, and url from [LiveKit Cloud](https://cloud.livekit.io)
- Go 1.19+ and Node.js

### Running Locally

To run locally, you'll need to run the two services in this repo: `meet` and `lkgt-service`.

Running Meet Locally:

In the `meet/` directory, copy `.env.example` to `.env.local` and fill in your LiveKit connection details. Then run:

```bash
# From the meet/ directory
yarn dev
```

Running lkgpt-service Locally:

```bash
# From the lkgpt-service/ directory
go run /cmd/server/main.go --config config.yaml --gcp-credentials-path gcp-credentials.json`
```

Once both services are running you can navigate to <http://localhost:3000>. There's one more step needed when running locally. When deployed, KITT is spawned via a LiveKit webhook, but locally - the webhook will have no way of reaching your local `lkgpt-service` that's running. So you'll have to manually call an API to spawn KITT:

```bash
# <room_name> comes from the url slug when you enter a room in the UI
curl -XPOST http://localhost:3001/join/<room_name>
```
