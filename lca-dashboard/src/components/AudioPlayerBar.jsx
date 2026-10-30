import React, { useState, useEffect } from 'react';

export default function AudioPlayer({ duration = 42, isLive = false }) {
  const [isPlaying, setIsPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(2);
  const [volume, setVolume] = useState(0.8);

  useEffect(() => {
    let interval;
    if (isPlaying) {
      interval = setInterval(() => {
        setCurrentTime(prev => {
          if (prev >= duration) { setIsPlaying(false); return 0; }
          return prev + 1;
        });
      }, 1000);
    }
    return () => clearInterval(interval);
  }, [isPlaying, duration]);

  const fmt = (s) => {
    const m = Math.floor(s / 60);
    const sec = Math.floor(s % 60);
    return `${m}:${sec.toString().padStart(2, '0')}`;
  };

  const pct = duration > 0 ? (currentTime / duration) * 100 : 0;

  return (
    <div className="audio-player">
      {/* Play/Pause */}
      <button className="audio-play-btn" onClick={() => setIsPlaying(!isPlaying)}>
        {isPlaying ? (
          <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
            <rect x="3" y="2" width="4" height="12" rx="1"/>
            <rect x="9" y="2" width="4" height="12" rx="1"/>
          </svg>
        ) : (
          <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
            <polygon points="3,1 15,8 3,15"/>
          </svg>
        )}
      </button>

      {/* Progress bar */}
      <div
        className="audio-progress"
        onClick={(e) => {
          const rect = e.currentTarget.getBoundingClientRect();
          const pct = (e.clientX - rect.left) / rect.width;
          setCurrentTime(Math.floor(pct * duration));
        }}
      >
        <div className="audio-progress-fill" style={{ width: `${pct}%` }} />
        <div className="audio-progress-handle" style={{ left: `${pct}%` }} />
      </div>

      {/* Time */}
      <span className="audio-time">{fmt(currentTime)} / {fmt(duration)}</span>

      {/* Volume */}
      <button className="audio-volume-btn" title="Toggle mute">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="#d5dbdb">
          <path d="M1 5v6h3l4 4V1L4 5H1z"/>
          <path d="M11.5 8A3.5 3.5 0 0 0 9 4.7v6.6A3.5 3.5 0 0 0 11.5 8z"/>
        </svg>
      </button>

      <input
        type="range"
        className="audio-volume-slider"
        min="0" max="1" step="0.05"
        value={volume}
        onChange={(e) => setVolume(parseFloat(e.target.value))}
        style={{ width: '60px' }}
      />
    </div>
  );
}
