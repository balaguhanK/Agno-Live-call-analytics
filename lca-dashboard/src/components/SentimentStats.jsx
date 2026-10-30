import React from 'react';

const TREND_ICONS = {
  Up: (
    <svg width="20" height="16" viewBox="0 0 20 16" fill="none">
      <polyline points="2,14 8,6 14,10 18,3" stroke="#1d8348" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" fill="none"/>
      <polyline points="14,3 18,3 18,7" stroke="#1d8348" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" fill="none"/>
    </svg>
  ),
  Down: (
    <svg width="20" height="16" viewBox="0 0 20 16" fill="none">
      <polyline points="2,3 8,11 14,7 18,14" stroke="#d13212" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" fill="none"/>
      <polyline points="14,14 18,14 18,10" stroke="#d13212" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" fill="none"/>
    </svg>
  ),
  Flat: (
    <svg width="20" height="16" viewBox="0 0 20 16" fill="none">
      <line x1="2" y1="8" x2="18" y2="8" stroke="#687078" strokeWidth="2.5" strokeLinecap="round"/>
      <polyline points="13,3 18,8 13,13" stroke="#687078" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" fill="none"/>
    </svg>
  ),
};

export default function SentimentStats({ call }) {
  const { callerAvgSentiment = 0, agentAvgSentiment = 0, callerSentimentTrend = 'Flat', agentSentimentTrend = 'Flat' } = call;

  const trendColor = { Up: '#1d8348', Down: '#d13212', Flat: '#687078' };

  return (
    <div className="sentiment-stats">
      {/* Caller */}
      <div className="sentiment-stat-row">
        <div className="sentiment-stat-label">Caller Average Sentiment (min: -5, max: +5)</div>
        <div className="sentiment-stat-score">
          <span>{callerAvgSentiment.toFixed(3)}</span>
        </div>
      </div>

      <div className="sentiment-stat-row">
        <div className="sentiment-stat-label">Caller Sentiment Trend</div>
        <div style={{ marginTop: '4px', color: trendColor[callerSentimentTrend] }}>
          {TREND_ICONS[callerSentimentTrend] || TREND_ICONS.Flat}
        </div>
      </div>

      {/* Agent */}
      <div className="sentiment-stat-row">
        <div className="sentiment-stat-label">Agent Average Sentiment (min: -5, max: +5)</div>
        <div className="sentiment-stat-score">
          <span>{agentAvgSentiment.toFixed(3)}</span>
        </div>
      </div>

      <div className="sentiment-stat-row">
        <div className="sentiment-stat-label">Agent Sentiment Trend</div>
        <div style={{ marginTop: '4px', color: trendColor[agentSentimentTrend] }}>
          {TREND_ICONS[agentSentimentTrend] || TREND_ICONS.Flat}
        </div>
      </div>
    </div>
  );
}
