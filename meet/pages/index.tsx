import { useRouter } from 'next/router';
import React, { useRef } from 'react';
import styles from '../styles/Home.module.css';

const Home = () => {
  const router = useRouter();
  const ref = useRef<HTMLSelectElement>(null);
  const startMeeting = () => {
    router.push({ pathname: `/rooms/${generateRoomId()}`, query: { languageCode: ref.current?.value } });
  };

  return (
    <main className={styles.main} data-lk-theme="default">
      <div className="header">
        {<img src="/images/livekit-meet-home.svg" alt="LiveKit Meet" width="360" height="45" />}
        <h2>Use ChatGPT with LiveKit</h2>
      </div>
      <div className={styles.startContainer}>
        <p style={{ marginTop: 0 }}>Try it now by creating a new room. Choose the language of the bot:</p>
        <select ref={ref} className={styles.startSelect}>
          <option value="en-US">English (United States)</option>
          <option value="fr-FR">French (France)</option>
          <option value="de-DE">German (Germany)</option>
          <option value="jp-JP">Japanese</option>
          <option value="cmn-CH">Mandarin Chinese</option>
          <option value="es-ES">Spanish</option>
        </select>
        <button className="lk-button" onClick={startMeeting}>
          Start Meeting
        </button>
      </div>
    </main>
  );
};

export default Home;

function generateRoomId(): string {
  return `${randomString(4)}-${randomString(4)}`;
}

function randomString(length: number): string {
  let result = '';
  const characters = 'abcdefghijklmnopqrstuvwxyz0123456789';
  const charactersLength = characters.length;
  for (let i = 0; i < length; i++) {
    result += characters.charAt(Math.floor(Math.random() * charactersLength));
  }
  return result;
}
