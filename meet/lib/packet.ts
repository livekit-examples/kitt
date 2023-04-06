export enum PacketType {
  Transcript = 0,
  State,
}

export enum GPTState {
  Idle = 0,
  Loading,
  Speaking,
}

export interface Packet {
  type: PacketType;
  data: TranscriptPacket | StatePacket;
}

export interface Duration {
  seconds?: number;
  nanos?: number;
}

export interface TranscriptPacket {
  sid: string;
  name: string;
  transcript: string;
  isFinal: boolean;
  resultEndTime: Duration;
}

export interface StatePacket {
  state: GPTState;
}

