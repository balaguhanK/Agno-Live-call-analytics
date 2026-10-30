import React, { useState } from 'react';
import ReactMarkdown from 'react-markdown';

export default function SummaryPanel({ summary }) {
  const [activeTab, setActiveTab] = useState('transcript');

  if (!summary) {
    return (
      <div className="cs-container" style={{ margin: '0 24px 16px' }}>
        <div className="cs-container-header">
          <span className="cs-container-title">
            Meeting Summary
            <a className="info-link" href="#" onClick={e => e.preventDefault()}>Info</a>
          </span>
        </div>
        <div className="cs-container-body" style={{ color: '#687078', textAlign: 'center', padding: '30px' }}>
          A summary is written automatically when the call ends.
        </div>
      </div>
    );
  }

  return (
    <div className="cs-container" style={{ margin: '0 24px 16px' }}>
      <div className="cs-container-header">
        <span className="cs-container-title">
          Meeting Summary
          <a className="info-link" href="#" onClick={e => e.preventDefault()}>Info</a>
        </span>
      </div>

      {/* Tabs */}
      <div className="summary-tabs">
        <div
          className={`summary-tab ${activeTab === 'transcript' ? 'active' : ''}`}
          onClick={() => setActiveTab('transcript')}
        >
          Transcript Summary
        </div>
      </div>

      {/* Summary content rendered as markdown */}
      <div className="summary-content">
        <ReactMarkdown>{summary.text}</ReactMarkdown>
      </div>
    </div>
  );
}
