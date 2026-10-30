import React, { useState } from 'react';
import { 
  Search, 
  Filter, 
  ExternalLink, 
  TrendingUp, 
  TrendingDown, 
  Minus, 
  Clock, 
  AlertTriangle,
  PhoneForwarded,
  CheckCircle,
  Play
} from 'lucide-react';

export default function SupervisorCallList({ calls, selectedCallId, onSelectCall }) {
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState('ALL'); // 'ALL' | 'IN_PROGRESS' | 'COMPLETED' | 'ALERTS'

  const filteredCalls = calls.filter((c) => {
    const matchesSearch =
      c.callerNumber.toLowerCase().includes(search.toLowerCase()) ||
      c.agentName.toLowerCase().includes(search.toLowerCase()) ||
      c.id.toLowerCase().includes(search.toLowerCase()) ||
      c.categoryAlerts.some((a) => a.toLowerCase().includes(search.toLowerCase()));

    if (!matchesSearch) return false;

    if (statusFilter === 'IN_PROGRESS') return c.status === 'IN_PROGRESS';
    if (statusFilter === 'COMPLETED') return c.status === 'COMPLETED';
    if (statusFilter === 'ALERTS') return c.categoryAlerts.length > 0;
    return true;
  });

  const getSentimentBadge = (trend, score) => {
    if (trend === 'POSITIVE' || score > 0.25) {
      return (
        <span className="badge" style={{ background: 'rgba(16, 185, 129, 0.15)', color: '#34d399', border: '1px solid rgba(16, 185, 129, 0.3)' }}>
          <TrendingUp size={12} />
          +{score.toFixed(2)} Positive
        </span>
      );
    }
    if (trend === 'NEGATIVE' || score < -0.25) {
      return (
        <span className="badge" style={{ background: 'rgba(244, 63, 94, 0.15)', color: '#fb7185', border: '1px solid rgba(244, 63, 94, 0.3)' }}>
          <TrendingDown size={12} />
          {score.toFixed(2)} Negative
        </span>
      );
    }
    return (
      <span className="badge" style={{ background: 'rgba(245, 158, 11, 0.15)', color: '#fbbf24', border: '1px solid rgba(245, 158, 11, 0.3)' }}>
        <Minus size={12} />
        {score.toFixed(2)} Neutral
      </span>
    );
  };

  const formatDuration = (secs) => {
    const m = Math.floor(secs / 60);
    const s = secs % 60;
    return `${m.toString().padStart(2, '0')}:${s.toString().padStart(2, '0')}`;
  };

  return (
    <div className="glass-panel" style={{ padding: '20px', display: 'flex', flexDirection: 'column', gap: '16px' }}>
      {/* Title & Toolbar */}
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between', gap: '12px' }}>
        <div>
          <h2 style={{ fontSize: '1.15rem', fontWeight: 700, fontFamily: 'var(--font-heading)' }}>
            Supervisor Call Monitoring & Real-time Alerts
          </h2>
          <p style={{ fontSize: '0.78rem', color: 'var(--text-muted)' }}>
            Live SIPREC call streams from FreeSWITCH PBX with real-time sentiment scoring and category triggers.
          </p>
        </div>

        {/* Filters & Search */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
          {/* Status Pills */}
          <div style={{ display: 'flex', background: 'rgba(0,0,0,0.3)', padding: '2px', borderRadius: 'var(--radius-sm)' }}>
            {['ALL', 'IN_PROGRESS', 'COMPLETED', 'ALERTS'].map((st) => (
              <button
                key={st}
                onClick={() => setStatusFilter(st)}
                style={{
                  background: statusFilter === st ? 'rgba(6, 182, 212, 0.2)' : 'transparent',
                  color: statusFilter === st ? '#38bdf8' : 'var(--text-muted)',
                  border: 'none',
                  padding: '4px 10px',
                  borderRadius: '4px',
                  fontSize: '0.72rem',
                  fontWeight: 600,
                  cursor: 'pointer'
                }}
              >
                {st.replace('_', ' ')}
              </button>
            ))}
          </div>

          {/* Search box */}
          <div style={{ position: 'relative', width: '220px' }}>
            <Search size={14} color="var(--text-muted)" style={{ position: 'absolute', left: '8px', top: '50%', transform: 'translateY(-50%)' }} />
            <input
              type="text"
              placeholder="Search by agent, phone, alert..."
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              style={{
                width: '100%',
                padding: '6px 8px 6px 28px',
                fontSize: '0.78rem',
                background: 'rgba(0,0,0,0.35)',
                border: '1px solid var(--border-subtle)',
                borderRadius: 'var(--radius-sm)',
                color: 'var(--text-primary)',
                outline: 'none'
              }}
            />
          </div>
        </div>
      </div>

      {/* Table */}
      <div style={{ overflowX: 'auto', border: '1px solid var(--border-subtle)', borderRadius: 'var(--radius-sm)' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: '0.82rem' }}>
          <thead>
            <tr style={{ background: 'rgba(255,255,255,0.02)', borderBottom: '1px solid var(--border-subtle)', color: 'var(--text-muted)', textTransform: 'uppercase', fontSize: '0.7rem' }}>
              <th style={{ padding: '10px 14px' }}>Call ID & Channel</th>
              <th style={{ padding: '10px 14px' }}>Caller</th>
              <th style={{ padding: '10px 14px' }}>Agent</th>
              <th style={{ padding: '10px 14px' }}>Duration</th>
              <th style={{ padding: '10px 14px' }}>Status</th>
              <th style={{ padding: '10px 14px' }}>Live Sentiment</th>
              <th style={{ padding: '10px 14px' }}>Category Alerts</th>
              <th style={{ padding: '10px 14px', textAlign: 'right' }}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {filteredCalls.map((call) => {
              const isSelected = call.id === selectedCallId;

              return (
                <tr
                  key={call.id}
                  onClick={() => onSelectCall(call.id)}
                  style={{
                    borderBottom: '1px solid var(--border-subtle)',
                    background: isSelected ? 'rgba(6, 182, 212, 0.08)' : 'transparent',
                    cursor: 'pointer',
                    transition: 'background 0.15s ease'
                  }}
                  onMouseEnter={(e) => {
                    if (!isSelected) e.currentTarget.style.background = 'rgba(255,255,255,0.03)';
                  }}
                  onMouseLeave={(e) => {
                    if (!isSelected) e.currentTarget.style.background = 'transparent';
                  }}
                >
                  {/* Call ID */}
                  <td style={{ padding: '12px 14px' }}>
                    <div style={{ fontWeight: 600, color: 'var(--text-primary)', fontFamily: 'var(--font-mono)', fontSize: '0.8rem' }}>
                      {call.id}
                    </div>
                    <div style={{ fontSize: '0.68rem', color: 'var(--text-muted)', fontFamily: 'var(--font-mono)' }}>
                      {call.siprecChannelId}
                    </div>
                  </td>

                  {/* Caller */}
                  <td style={{ padding: '12px 14px', color: '#22d3ee', fontWeight: 500 }}>
                    {call.callerNumber}
                  </td>

                  {/* Agent */}
                  <td style={{ padding: '12px 14px' }}>
                    <div style={{ fontWeight: 500, color: 'var(--text-primary)' }}>{call.agentName}</div>
                    <div style={{ fontSize: '0.7rem', color: 'var(--text-muted)' }}>Ext. {call.agentExtension}</div>
                  </td>

                  {/* Duration */}
                  <td style={{ padding: '12px 14px', fontFamily: 'var(--font-mono)', color: 'var(--text-secondary)' }}>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
                      <Clock size={12} />
                      {formatDuration(call.elapsedSeconds)}
                    </span>
                  </td>

                  {/* Status */}
                  <td style={{ padding: '12px 14px' }}>
                    {call.status === 'IN_PROGRESS' ? (
                      <span className="badge badge-live">
                        <span style={{ width: '5px', height: '5px', borderRadius: '50%', background: '#10b981' }} className="animate-pulse-live" />
                        In Progress
                      </span>
                    ) : (
                      <span className="badge badge-completed">
                        Completed
                      </span>
                    )}
                  </td>

                  {/* Sentiment */}
                  <td style={{ padding: '12px 14px' }}>
                    {getSentimentBadge(call.sentimentTrend, call.sentimentScore)}
                  </td>

                  {/* Category Alerts */}
                  <td style={{ padding: '12px 14px' }}>
                    <div style={{ display: 'flex', flexWrap: 'wrap', gap: '4px' }}>
                      {call.categoryAlerts.map((cat, idx) => (
                        <span
                          key={idx}
                          className={cat.includes('DISPUTE') || cat.includes('RISK') ? 'badge badge-alert' : 'badge badge-info'}
                          style={{ fontSize: '0.65rem' }}
                        >
                          {cat}
                        </span>
                      ))}
                    </div>
                  </td>

                  {/* Actions */}
                  <td style={{ padding: '12px 14px', textAlign: 'right' }}>
                    <button
                      className="btn btn-secondary"
                      style={{ padding: '4px 9px', fontSize: '0.75rem' }}
                      onClick={(e) => {
                        e.stopPropagation();
                        onSelectCall(call.id);
                      }}
                    >
                      Open Console
                      <ExternalLink size={12} />
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
