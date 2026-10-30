import React from 'react';
import CallInsights from './CallInsights';

// Sentiment is analysed after the call ends, over the whole transcript.
// While a call is live this shows a note instead of empty charts.
// embedded renders just the body, for use inside another section.
export default function PostCallAnalysis({ call, embedded = false }) {
  const pending = call.status === 'In Progress';
  const body = (
    <div className="cs-container-body">
      {pending ? (
        <div className="empty-state" role="status">
          <strong>Sentiment analysis runs when the call ends</strong>
          <span>You'll see how the caller felt, whether their query was resolved, and how the agent spoke.</span>
        </div>
      ) : (
        <CallInsights call={call} />
      )}
    </div>
  );
  if (embedded) return body;
  return (
    <div className="cs-container">
      <div className="cs-container-header">
        <span className="cs-container-title">Post-call analysis</span>
      </div>
      {body}
    </div>
  );
}
