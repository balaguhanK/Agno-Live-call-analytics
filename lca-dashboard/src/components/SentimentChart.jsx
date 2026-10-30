import React from 'react';

const MAX_TIME_LABELS = 5;

/**
 * SVG "Sentiment Fluctuation" chart: POSITIVE/NEUTRAL/NEGATIVE Y-axis labels,
 * blue=AGENT line, red=CALLER line, X=Time.
 */
export default function SentimentCharts({ sentimentHistory }) {
  const W = 340;
  const H = 180;
  const PAD = { top: 16, right: 16, bottom: 36, left: 72 };
  const IW = W - PAD.left - PAD.right;
  const IH = H - PAD.top - PAD.bottom;

  const data = sentimentHistory || [];
  const n = data.length;
  // Label at most MAX_TIME_LABELS points so the times don't overlap
  const labelStep = Math.max(1, Math.ceil(n / MAX_TIME_LABELS));

  // Score range: -5 to +5 mapped to NEGATIVE/NEUTRAL/POSITIVE
  const scoreToY = (s) => {
    // clamp -5 to +5
    const clamped = Math.max(-5, Math.min(5, s));
    // +5 = top (PAD.top), -5 = bottom (PAD.top+IH)
    return PAD.top + IH * ((5 - clamped) / 10);
  };

  const timeToX = (i) => {
    if (n <= 1) return PAD.left + IW / 2;
    return PAD.left + (i / (n - 1)) * IW;
  };

  const agentPoints = data.map((d, i) => `${timeToX(i)},${scoreToY(d.agent)}`).join(' ');
  const callerPoints = data.map((d, i) => `${timeToX(i)},${scoreToY(d.caller)}`).join(' ');

  const neutralY = scoreToY(0);
  const positiveY = scoreToY(5);
  const negativeY = scoreToY(-5);

  return (
    <div style={{ display: 'flex', gap: '8px', alignItems: 'flex-start' }}>
      {/* Chart 1: Sentiment Fluctuation */}
      <div style={{ flex: 1 }}>
        <div className="chart-title">Sentiment Fluctuation</div>
        <svg viewBox={`0 0 ${W} ${H}`} className="chart-canvas" style={{ display: 'block', overflow: 'visible' }}>
          {/* Y-axis labels */}
          <text x={PAD.left - 8} y={positiveY + 4} textAnchor="end" fontSize="11" fill="#687078">POSITIVE</text>
          <text x={PAD.left - 8} y={neutralY + 4} textAnchor="end" fontSize="11" fill="#687078">NEUTRAL</text>
          <text x={PAD.left - 8} y={negativeY + 4} textAnchor="end" fontSize="11" fill="#687078">NEGATIVE</text>

          {/* Grid lines */}
          <line x1={PAD.left} y1={positiveY} x2={PAD.left + IW} y2={positiveY} stroke="#eaeded" strokeWidth="1" />
          <line x1={PAD.left} y1={neutralY} x2={PAD.left + IW} y2={neutralY} stroke="#aab7b8" strokeWidth="1" strokeDasharray="4 2" />
          <line x1={PAD.left} y1={negativeY} x2={PAD.left + IW} y2={negativeY} stroke="#eaeded" strokeWidth="1" />

          {/* X-axis time labels */}
          {data.map((d, i) => (i % labelStep === 0 || i === n - 1) && (
            <text key={i} x={timeToX(i)} y={H - 16} textAnchor="middle" fontSize="11" fill="#687078">{d.time}</text>
          ))}

          {/* X-axis label */}
          <text x={PAD.left + IW / 2} y={H + 4} textAnchor="middle" fontSize="12" fill="#687078" fontWeight="600">Time</text>

          {/* Agent line (blue) */}
          {n > 1 && (
            <polyline fill="none" stroke="#0073bb" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" points={agentPoints} />
          )}

          {/* Caller line (red/crimson) */}
          {n > 1 && (
            <polyline fill="none" stroke="#c0392b" strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" points={callerPoints} />
          )}

          {/* Data dots */}
          {data.map((d, i) => (
            <g key={i}>
              <circle cx={timeToX(i)} cy={scoreToY(d.agent)} r={4} fill="#0073bb" />
              <circle cx={timeToX(i)} cy={scoreToY(d.caller)} r={4} fill="#c0392b" />
            </g>
          ))}
        </svg>
        <div className="chart-legend">
          <div className="chart-legend-item">
            <div className="chart-legend-line" style={{ background: '#0073bb' }} />
            <span>AGENT</span>
          </div>
          <div className="chart-legend-item">
            <div className="chart-legend-line" style={{ background: '#c0392b' }} />
            <span>CALLER</span>
          </div>
        </div>
      </div>

    </div>
  );
}
