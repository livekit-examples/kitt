import { useRouter } from 'next/router';
import React from 'react';
import styles from '../styles/Home.module.css';

const Home = () => {
  const router = useRouter();
  const startMeeting = () => {
    router.push(`/rooms/${generateRoomId()}`);
  };

  return (
    <main className={styles.main} data-lk-theme="default">
      <div className="header">
        {<img src="/images/livekit-meet-home.svg" alt="LiveKit Meet" width="360" height="45" />}
        <h2>Use ChatGPT with LiveKit</h2>
      </div>
      <div className={styles.startContainer}>
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
