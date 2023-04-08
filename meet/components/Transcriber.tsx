import { Box, Text } from '@chakra-ui/react';
import { useDataChannel } from '@livekit/components-react';
import { useEffect, useState } from 'react';
import { GPTState, Packet, PacketType, StatePacket, TranscriptPacket } from '../lib/packet';

export const Transcriber = () => {
  const { message } = useDataChannel();
  const [visible, setVisible] = useState<boolean>(false);

  const [statePacket, setStatePacket] = useState<StatePacket>();
  const [transcriptPacket, setTranscriptPacket] = useState<TranscriptPacket>();

  // Transcription of every participant in the room
  const [transcripts, setTranscripts] = useState<Map<string, string>>(new Map());

  useEffect(() => {
    if (!message) return;

    const decoder = new TextDecoder();
    const packet = JSON.parse(decoder.decode(message.payload)) as Packet;
    if (packet.type == PacketType.Transcript) {
      setTranscriptPacket(packet.data as TranscriptPacket);
    } else if (packet.type == PacketType.State) {
      setStatePacket(packet.data as StatePacket);
    }
  }, [message]);

  useEffect(() => {
    if (!transcriptPacket) return;

    setTranscripts(
      new Map(
        transcripts.set(transcriptPacket.sid, transcriptPacket.name + ': ' + transcriptPacket.text),
      ),
    );

    if (statePacket?.state == GPTState.Active) setVisible(true);

    const timeout = setTimeout(() => {
      setTranscripts(new Map());

      setVisible(false);
    }, 5000);

    return () => clearTimeout(timeout);
  }, [transcriptPacket, statePacket]);

  return visible ? (
    <Box
      position="fixed"
      left="50%"
      transform="translateX(-50%)"
      paddingX="4px"
      bottom="8rem"
      bgColor="rgba(255, 255, 255, 0.12)"
    >
      {Array.from(transcripts.entries()).map((entry) => {
        const [key, value] = entry;
        return (
          <Text margin={0} key={key}>
            {value}
          </Text>
        );
      })}
    </Box>
  ) : (
    <> </>
  );
};
