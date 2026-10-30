import React, { useState } from 'react';

// Series colours shared with the live dashboard (validated as a pair for
// colour-blind separation and contrast). Text never uses them.
const SERIES = {
  agent: { label: 'Agent', color: '#0073bb' },
  caller: { label: 'Customer', color: '#c0392b' },
  silence: { label: 'Silence', color: '#c6ccd0' },
  interruption: { label: 'Interruption', color: '#414d5c' },
};

const GRID = '#eaeded';
const AXIS_TEXT = '#5f6b7a';
const SENTIMENT_MAX = 5;

export function Legend({ keys }) {
  return (
    <ul className="pca-legend" aria-label="Legend">
      {keys.map(k => (
        <li key={k}>
          <span className={`pca-swatch ${k}`} style={{ background: k === 'interruption' ? undefined : SERIES[k].color }} aria-hidden="true" />
          {SERIES[k].label}
        </li>
      ))}
    </ul>
  );
}

// Text under the chart describing the hovered or focused point
function Readout({ text }) {
  return <div className="pca-readout" aria-live="polite">{text || ' '}</div>;
}

const fmtScore = (v) => (v === null ? 'no lines' : `${v > 0 ? '+' : ''}${v.toFixed(2)}`);

/** Average sentiment per call quarter, agent and customer lines (-5..+5). */
export function QuarterSentimentChart({ data }) {
  const [hover, setHover] = useState(null);
  const W = 320, H = 170;
  const PAD = { top: 10, right: 12, bottom: 26, left: 30 };
  const IW = W - PAD.left - PAD.right;
  const IH = H - PAD.top - PAD.bottom;
  const x = (i) => PAD.left + (data.length <= 1 ? IW / 2 : (i / (data.length - 1)) * IW);
  const y = (v) => PAD.top + IH * ((SENTIMENT_MAX - v) / (2 * SENTIMENT_MAX));
  const path = (key) => {
    let d = '';
    let pen = false;
    data.forEach((p, i) => {
      if (p[key] === null) { pen = false; return; }
      d += `${pen ? 'L' : 'M'}${x(i)},${y(p[key])} `;
      pen = true;
    });
    return d;
  };
  const point = hover === null ? null : data[hover];
  return (
    <figure className="pca-chart">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Sentiment per call quarter for agent and customer">
        {[SENTIMENT_MAX, 0, -SENTIMENT_MAX].map(v => (
          <g key={v}>
            <line x1={PAD.left} x2={W - PAD.right} y1={y(v)} y2={y(v)} stroke={v === 0 ? '#aab7b8' : GRID} strokeDasharray={v === 0 ? '4 3' : undefined} />
            <text x={PAD.left - 6} y={y(v) + 4} textAnchor="end" fontSize="11" fill={AXIS_TEXT}>{v > 0 ? `+${v}` : v}</text>
          </g>
        ))}
        {data.map((p, i) => <text key={p.quarter} x={x(i)} y={H - 8} textAnchor="middle" fontSize="11" fill={AXIS_TEXT}>{p.quarter}</text>)}
        {['agent', 'caller'].map(k => (
          <g key={k}>
            <path d={path(k)} fill="none" stroke={SERIES[k].color} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" />
            {data.map((p, i) => p[k] !== null && (
              <circle key={i} cx={x(i)} cy={y(p[k])} r={hover === i ? 5 : 4} fill={SERIES[k].color} stroke="#fff" strokeWidth="2" />
            ))}
          </g>
        ))}
        {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={PAD.top + IH} stroke="#879596" strokeWidth="1" />}
        {data.map((p, i) => (
          <rect
            key={`hit-${i}`}
            x={x(i) - IW / (2 * Math.max(1, data.length - 1))}
            y={PAD.top}
            width={IW / Math.max(1, data.length - 1)}
            height={IH}
            fill="transparent"
            tabIndex={0}
            aria-label={`${p.quarter}: agent ${fmtScore(p.agent)}, customer ${fmtScore(p.caller)}`}
            onMouseEnter={() => setHover(i)}
            onMouseLeave={() => setHover(null)}
            onFocus={() => setHover(i)}
            onBlur={() => setHover(null)}
          />
        ))}
      </svg>
      <Legend keys={['agent', 'caller']} />
      <Readout text={point && `${point.quarter}: agent ${fmtScore(point.agent)}, customer ${fmtScore(point.caller)}`} />
    </figure>
  );
}

const fmtSec = (s) => `${Math.floor(s / 60)}:${String(Math.round(s % 60)).padStart(2, '0')}`;

/** Share of the call each speaker talked, and silence, as one stacked bar. */
export function SpeakerTimeChart({ time }) {
  const [hover, setHover] = useState(null);
  const parts = [
    { key: 'agent', pct: time.agentPct, sec: time.agentSec },
    { key: 'caller', pct: time.callerPct, sec: time.callerSec },
    { key: 'silence', pct: time.silencePct, sec: time.silenceSec },
  ].filter(p => p.pct > 0);
  const describe = (p) => `${SERIES[p.key].label}: ${p.pct}% (${fmtSec(p.sec)})`;
  return (
    <figure className="pca-chart">
      <div className="pca-stack" role="img" aria-label={parts.map(describe).join(', ') || 'No speech'}>
        {parts.map(p => (
          <span
            key={p.key}
            className="pca-stack-part"
            style={{ width: `${p.pct}%`, background: SERIES[p.key].color }}
            tabIndex={0}
            aria-label={describe(p)}
            onMouseEnter={() => setHover(p)}
            onMouseLeave={() => setHover(null)}
            onFocus={() => setHover(p)}
            onBlur={() => setHover(null)}
          >
            {p.pct >= 12 && <span className={`pca-stack-label${p.key === 'silence' ? ' dark' : ''}`}>{p.pct}%</span>}
          </span>
        ))}
      </div>
      <Legend keys={['agent', 'caller', 'silence']} />
      <Readout text={hover && describe(hover)} />
    </figure>
  );
}

/** Bar chart comparing talk time for Agent, Customer, and Silence. */
export function SpeakerBarChart({ time, stats }) {
  const [hover, setHover] = useState(null);
  const W = 360, H = 140;
  const PAD = { top: 22, right: 16, bottom: 26, left: 40 };
  const IW = W - PAD.left - PAD.right;
  const IH = H - PAD.top - PAD.bottom;

  const items = [
    {
      key: 'agent',
      label: 'Agent',
      sec: time.agentSec || 0,
      pct: time.agentPct || 0,
      lines: stats?.AGENT?.lines,
      words: stats?.AGENT?.words,
      longest: stats?.AGENT?.longest,
    },
    {
      key: 'caller',
      label: 'Customer',
      sec: time.callerSec || 0,
      pct: time.callerPct || 0,
      lines: stats?.CUSTOMER?.lines,
      words: stats?.CUSTOMER?.words,
      longest: stats?.CUSTOMER?.longest,
    },
    {
      key: 'silence',
      label: 'Silence',
      sec: time.silenceSec || 0,
      pct: time.silencePct || 0,
    },
  ];

  const maxSec = Math.max(1, ...items.map(d => d.sec));
  const niceMax = Math.max(10, Math.ceil(maxSec / 10) * 10);
  const ticks = [niceMax, Math.round(niceMax / 2), 0];

  const y = (sec) => PAD.top + IH * (1 - sec / niceMax);

  const slotWidth = IW / items.length;
  const barWidth = Math.min(48, slotWidth * 0.52);

  const point = hover === null ? null : items[hover];
  const describe = (d) => {
    let s = `${d.label}: ${fmtSec(d.sec)} (${d.pct}%)`;
    if (d.lines !== undefined) s += ` · ${d.lines} lines · ${d.words} words · longest turn ${fmtSec(d.longest || 0)}`;
    return s;
  };

  return (
    <figure className="pca-chart speaker-bar-figure">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Talk time per speaker bar chart">
        {ticks.map(v => (
          <g key={v}>
            <line x1={PAD.left} x2={W - PAD.right} y1={y(v)} y2={y(v)} stroke={GRID} strokeDasharray={v === 0 ? undefined : "3 3"} />
            <text x={PAD.left - 6} y={y(v) + 3} textAnchor="end" fontSize="10" fill={AXIS_TEXT}>{fmtSec(v)}</text>
          </g>
        ))}
        {items.map((item, i) => {
          const cx = PAD.left + (i + 0.5) * slotWidth;
          const bx = cx - barWidth / 2;
          const by = y(item.sec);
          const bh = Math.max(2, IH * (item.sec / niceMax));
          const isHovered = hover === i;
          return (
            <g key={item.key}>
              <rect
                x={bx}
                y={by}
                width={barWidth}
                height={bh}
                rx="3"
                fill={SERIES[item.key].color}
                opacity={hover === null || isHovered ? 1 : 0.55}
              />
              <text
                x={cx}
                y={by - 6}
                textAnchor="middle"
                fontSize="11"
                fontWeight="700"
                fill="#16191f"
              >
                {fmtSec(item.sec)}
              </text>
              <text
                x={cx}
                y={PAD.top + IH + 16}
                textAnchor="middle"
                fontSize="11"
                fontWeight="600"
                fill={AXIS_TEXT}
              >
                {item.label}
              </text>
              <rect
                x={PAD.left + i * slotWidth}
                y={PAD.top}
                width={slotWidth}
                height={IH + 20}
                fill="transparent"
                tabIndex={0}
                aria-label={describe(item)}
                onMouseEnter={() => setHover(i)}
                onMouseLeave={() => setHover(null)}
                onFocus={() => setHover(i)}
                onBlur={() => setHover(null)}
              />
            </g>
          );
        })}
      </svg>
      <Legend keys={['agent', 'caller', 'silence']} />
      <Readout text={point && describe(point)} />
    </figure>
  );
}

/** Loudness per second of the recording, coloured by who was speaking. */
export function LoudnessChart({ data }) {
  const [hover, setHover] = useState(null);
  const W = 960, H = 200;
  const PAD = { top: 10, right: 8, bottom: 28, left: 36 };
  const IW = W - PAD.left - PAD.right;
  const IH = H - PAD.top - PAD.bottom;
  const n = Math.max(1, data.length);
  const bw = IW / n;
  const gap = bw > 4 ? 1 : 0;
  const y = (v) => PAD.top + IH * (1 - v / 100);
  const tickEvery = Math.max(10, Math.ceil(n / 8 / 10) * 10);
  const point = hover === null ? null : data[hover];
  return (
    <figure className="pca-chart">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Loudness of the recording per second, by speaker">
        <defs>
          <pattern id="pca-hatch" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
            <rect width="6" height="6" fill="#fff" />
            <line x1="0" y1="0" x2="0" y2="6" stroke={SERIES.interruption.color} strokeWidth="3" />
          </pattern>
        </defs>
        {[0, 50, 100].map(v => (
          <g key={v}>
            <line x1={PAD.left} x2={W - PAD.right} y1={y(v)} y2={y(v)} stroke={GRID} />
            <text x={PAD.left - 6} y={y(v) + 4} textAnchor="end" fontSize="11" fill={AXIS_TEXT}>{v}</text>
          </g>
        ))}
        {data.map((d, i) => {
          const h = Math.max(d.kind === 'silence' ? 2 : 1, IH * (d.level / 100));
          return (
            <rect
              key={d.second}
              x={PAD.left + i * bw + gap / 2}
              y={PAD.top + IH - h}
              width={Math.max(0.5, bw - gap)}
              height={h}
              fill={d.kind === 'interruption' ? 'url(#pca-hatch)' : SERIES[d.kind].color}
              opacity={hover === null || hover === i ? 1 : 0.55}
            />
          );
        })}
        {data.map((d, i) => i % tickEvery === 0 && (
          <text key={`t${i}`} x={PAD.left + i * bw} y={H - 8} textAnchor="middle" fontSize="11" fill={AXIS_TEXT}>{fmtSec(d.second)}</text>
        ))}
        <rect
          x={PAD.left}
          y={PAD.top}
          width={IW}
          height={IH}
          fill="transparent"
          onMouseMove={(e) => {
            const box = e.currentTarget.getBoundingClientRect();
            const i = Math.floor(((e.clientX - box.left) / box.width) * n);
            setHover(Math.max(0, Math.min(n - 1, i)));
          }}
          onMouseLeave={() => setHover(null)}
        />
      </svg>
      <Legend keys={['agent', 'caller', 'interruption', 'silence']} />
      <Readout text={point && `${fmtSec(point.second)}: ${SERIES[point.kind].label.toLowerCase()}, level ${point.level}`} />
    </figure>
  );
}
