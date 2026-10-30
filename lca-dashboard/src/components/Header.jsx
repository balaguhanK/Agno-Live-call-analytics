import React from 'react';
import { 
  PhoneCall, 
  Activity, 
  Database, 
  Radio, 
  Play, 
  Pause, 
  FastForward, 
  PlusCircle, 
  Grid, 
  LayoutDashboard,
  ShieldCheck,
  AlertCircle
} from 'lucide-react';

export default function Header({ 
  activeView, 
  setActiveView, 
  calls, 
  isSimulating, 
  toggleSimulation, 
  triggerNextTurn, 
  addNewSimulatedCall,
  activeAlertCount 
}) {
  const inProgressCalls = calls.filter(c => c.status === 'IN_PROGRESS').length;
  
  return (
    <header className="glass-panel" style={{
      margin: '12px 16px 16px 16px',
      padding: '12px 20px',
      display: 'flex',
      flexWrap: 'wrap',
      alignItems: 'center',
      justifyContent: 'space-between',
      gap: '16px',
      position: 'sticky',
      top: '12px',
      zIndex: 50
    }}>
      {/* Brand & Platform Badges */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
        <div style={{
          width: '42px',
          height: '42px',
          borderRadius: '10px',
          background: 'linear-gradient(135deg, #06b6d4, #3b82f6)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          boxShadow: '0 0 16px rgba(6, 182, 212, 0.4)'
        }}>
          <PhoneCall size={22} color="#ffffff" />
        </div>
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <h1 style={{ 
              fontSize: '1.25rem', 
              fontWeight: '700', 
              fontFamily: 'var(--font-heading)',
              letterSpacing: '-0.02em',
              background: 'linear-gradient(to right, #f8fafc, #94a3b8)',
              WebkitBackgroundClip: 'text',
              WebkitTextFillColor: 'transparent'
            }}>
              Live Call Analytics
            </h1>
            <span className="badge badge-ai" style={{ fontSize: '0.68rem', padding: '2px 6px' }}>
              FreeSWITCH + Postgres
            </span>
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginTop: '2px' }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: '5px', fontSize: '0.75rem', color: '#34d399' }}>
              <span style={{ width: '6px', height: '6px', borderRadius: '50%', background: '#10b981', display: 'inline-block' }} className="animate-pulse-live" />
              SIPREC: Active
            </span>
            <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>•</span>
            <span style={{ display: 'flex', alignItems: 'center', gap: '5px', fontSize: '0.75rem', color: '#38bdf8' }}>
              <Radio size={12} />
              Whisper ASR: 16kHz
            </span>
            <span style={{ fontSize: '0.75rem', color: 'var(--text-muted)' }}>•</span>
            <span style={{ display: 'flex', alignItems: 'center', gap: '5px', fontSize: '0.75rem', color: '#c084fc' }}>
              <Database size={12} />
              pgvector: Ready
            </span>
          </div>
        </div>
      </div>

      {/* KPI Stats */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '18px', padding: '0 12px' }}>
        <div style={{ textAlign: 'center' }}>
          <div style={{ fontSize: '0.7rem', color: 'var(--text-muted)', textTransform: 'uppercase', fontWeight: 600 }}>Active Calls</div>
          <div style={{ fontSize: '1.15rem', fontWeight: 700, color: '#34d399', fontFamily: 'var(--font-mono)' }}>
            {inProgressCalls}
          </div>
        </div>
        <div style={{ width: '1px', height: '24px', background: 'var(--border-subtle)' }} />
        <div style={{ textAlign: 'center' }}>
          <div style={{ fontSize: '0.7rem', color: 'var(--text-muted)', textTransform: 'uppercase', fontWeight: 600 }}>Total Calls</div>
          <div style={{ fontSize: '1.15rem', fontWeight: 700, color: 'var(--text-primary)', fontFamily: 'var(--font-mono)' }}>
            {calls.length}
          </div>
        </div>
        <div style={{ width: '1px', height: '24px', background: 'var(--border-subtle)' }} />
        <div style={{ textAlign: 'center' }}>
          <div style={{ fontSize: '0.7rem', color: 'var(--text-muted)', textTransform: 'uppercase', fontWeight: 600 }}>Active Alerts</div>
          <div style={{ 
            fontSize: '1.15rem', 
            fontWeight: 700, 
            color: activeAlertCount > 0 ? '#fb7185' : '#94a3b8', 
            fontFamily: 'var(--font-mono)' 
          }}>
            {activeAlertCount}
          </div>
        </div>
      </div>

      {/* View Switcher & Live Simulation Controls */}
      <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
        {/* Navigation View Buttons */}
        <div style={{
          display: 'flex',
          background: 'rgba(0,0,0,0.35)',
          padding: '3px',
          borderRadius: 'var(--radius-sm)',
          border: '1px solid var(--border-subtle)'
        }}>
          <button 
            className="btn"
            style={{
              padding: '5px 12px',
              fontSize: '0.8rem',
              background: activeView === 'console' ? 'rgba(6, 182, 212, 0.2)' : 'transparent',
              color: activeView === 'console' ? '#38bdf8' : 'var(--text-secondary)',
              border: activeView === 'console' ? '1px solid rgba(6, 182, 212, 0.4)' : '1px solid transparent'
            }}
            onClick={() => setActiveView('console')}
          >
            <LayoutDashboard size={14} />
            Call Console
          </button>
          <button 
            className="btn"
            style={{
              padding: '5px 12px',
              fontSize: '0.8rem',
              background: activeView === 'list' ? 'rgba(6, 182, 212, 0.2)' : 'transparent',
              color: activeView === 'list' ? '#38bdf8' : 'var(--text-secondary)',
              border: activeView === 'list' ? '1px solid rgba(6, 182, 212, 0.4)' : '1px solid transparent'
            }}
            onClick={() => setActiveView('list')}
          >
            <Grid size={14} />
            Supervisor Grid
          </button>
        </div>

        {/* Live Simulator Toolbar */}
        <div style={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
          <button 
            className="btn btn-primary"
            style={{ fontSize: '0.78rem', padding: '6px 11px' }}
            title="Inject next real-time speech turn via SIPREC"
            onClick={triggerNextTurn}
          >
            <FastForward size={14} />
            Next Speech Turn
          </button>

          <button 
            className="btn btn-secondary"
            style={{ 
              fontSize: '0.78rem', 
              padding: '6px 11px',
              background: isSimulating ? 'rgba(244, 63, 94, 0.15)' : 'rgba(16, 185, 129, 0.15)',
              borderColor: isSimulating ? 'rgba(244, 63, 94, 0.3)' : 'rgba(16, 185, 129, 0.3)',
              color: isSimulating ? '#fb7185' : '#34d399'
            }}
            onClick={toggleSimulation}
          >
            {isSimulating ? <Pause size={14} /> : <Play size={14} />}
            {isSimulating ? 'Pause Stream' : 'Auto Stream'}
          </button>

          <button 
            className="btn btn-secondary"
            style={{ fontSize: '0.78rem', padding: '6px 10px' }}
            title="Simulate new inbound SIP call from FreeSWITCH"
            onClick={addNewSimulatedCall}
          >
            <PlusCircle size={14} />
            New Call
          </button>
        </div>
      </div>
    </header>
  );
}
