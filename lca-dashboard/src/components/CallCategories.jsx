import React from 'react';

// Call categories detected from the transcript, as tags
export default function CallCategories({ categories = [] }) {
  const visibleCategories = categories.filter(c => c !== 'HowCanIHelp');
  return (
    <div className="cs-container">
      <div className="cs-container-header">
        <span className="cs-container-title">
          Call Categories
          <a className="info-link" href="#" onClick={e => e.preventDefault()}>Info</a>
        </span>
      </div>
      <div className="cs-container-body">
        {visibleCategories.length === 0 ? (
          <p className="insight-note">Categories are added when the call ends.</p>
        ) : (
          <ul className="category-tags" aria-label="Call categories">
            {visibleCategories.map(c => <li key={c} className="category-tag">{c}</li>)}
          </ul>
        )}
      </div>
    </div>
  );
}
