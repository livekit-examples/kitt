import { Box, Text } from '@chakra-ui/react';
import { useDataChannel } from '@livekit/components-react';
import { useEffect, useState } from 'react';
import { Packet, PacketType, TranscriptPacket } from '../lib/packet';


export const Transcriber = () => {
  const { message } = useDataChannel();
  const [visible, setVisible] = useState<boolean>(false);
  const [packet, setPacket] = useState<TranscriptPacket>();
  const [transcripts, setTranscripts] = useState<Map<string, string>>(new Map());

  useEffect(() => {
    if (!message)
      return;

    const decoder = new TextDecoder();
    const packet = JSON.parse(decoder.decode(message.payload)) as Packet;
    if (packet.type == PacketType.Transcript) {
      const transcriptPacket = packet.data as TranscriptPacket;
      setPacket(transcriptPacket);
    }
  }, [message]);

  useEffect(() => {
    if (!packet)
      return;

    setTranscripts(new Map(transcripts.set(packet.sid, packet.name + ': ' + packet.transcript)));

    setVisible(true);
    const timeout = setTimeout(() => {
      transcripts.delete(packet.sid);
      setTranscripts(new Map(transcripts));
      setVisible(false);
    }, 5000);

    return () => clearTimeout(timeout);
  }, [packet]);

  return visible && packet ? (
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
