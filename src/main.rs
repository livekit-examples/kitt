use anyhow::Result;
use dotenv::dotenv;
use env_logger;
use livekit::prelude::*;
use log::trace;
use reqwest::{self, header};
use serde::{Deserialize, Serialize};
use std::env;

const DEV_URL: &'static str = "ws://localhost:2770";
const DEV_TOKEN: &'static str = "<put token here>";
const GPT_COMPLETION_URL: &'static str = "https://api.openai.com/v1/chat/completions";

#[derive(Debug, Clone, Serialize, Deserialize)]
struct ChatMessage {
    role: String,
    content: String,
}

#[derive(Debug, Clone, Deserialize)]
struct ChatChoice {
    index: i64,
    message: ChatMessage,
    finish_reason: String,
}

#[derive(Debug, Clone, Serialize)]
struct ChatRequest {
    model: String,
    messages: Vec<ChatMessage>,
}

#[derive(Debug, Clone, Deserialize)]
struct ChatResponse {
    id: String,
    object: String,
    created: i64,
    choices: Vec<ChatChoice>,
}

async fn request_chat(client: reqwest::Client) -> Result<ChatResponse> {
    let chat_req = ChatRequest {
        model: "gpt-3.5-turbo".to_string(),
        messages: vec![ChatMessage {
            role: "user".to_string(),
            content: "Hello".to_string(),
        }],
    };

    let auth_token = format!("Bearer {}", env::var("GPT_API_KEY").unwrap());
    let res = client
        .post(GPT_COMPLETION_URL)
        .header(header::AUTHORIZATION, auth_token)
        .json(&chat_req)
        .send()
        .await?;

    trace!("received response: {:?}", res);
    Ok(res.json().await?)
}

#[tokio::main]
async fn main() {

    env_logger::init();
    dotenv().ok();
    dbg!(request_chat(reqwest::Client::new()).await.unwrap());
}
