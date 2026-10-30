import React, { useState } from 'react';
import { AudioLines, UserCheck, Globe, FileText, ArrowRight } from 'lucide-react';
import { signIn, DEMO_ACCOUNTS } from '../auth/authService';
import './LoginPage.css';

const ROLES = {
  agent: {
    label: 'Agent',
    description: 'Agents answer live calls from their inbox with real-time captions and assist.',
    demo: DEMO_ACCOUNTS.agent,
  },
  admin: {
    label: 'Admin',
    description: 'Admins manage agents and review call analytics, transcripts and summaries.',
    demo: DEMO_ACCOUNTS.admin,
  },
};

const FEATURES = [
  { icon: UserCheck, title: 'Talk to a real person', text: 'Customers are connected to a free support agent in seconds.' },
  { icon: Globe, title: 'Live captions multiple language' },
  { icon: FileText, title: 'Every call transcribed', text: 'Transcripts are saved for the customer and the support team.' },
];

export default function LoginPage({ onSignIn }) {
  const [role, setRole] = useState('agent');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [remember, setRemember] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const current = ROLES[role];

  const switchRole = (next) => {
    setRole(next);
    setError('');
    setNotice('');
  };

  const handleSubmit = async (e) => {
    e.preventDefault();
    setNotice('');
    if (!/^\S+@\S+\.\S+$/.test(email)) {
      setError('Enter a valid work email.');
      return;
    }
    if (!password) {
      setError('Enter your password.');
      return;
    }
    setError('');
    setSubmitting(true);
    try {
      onSignIn(await signIn({ email, password, role, remember }));
    } catch (err) {
      setError(err.message);
      setSubmitting(false);
    }
  };

  const fillDemo = () => {
    setEmail(current.demo.email);
    setPassword(current.demo.password);
    setError('');
  };

  return (
    <div className="login-page">
      <div className="login-card">
        {/* ── Brand panel ── */}
        <section className="login-brand">
          <div className="login-logo">
            <span className="login-logo-mark"><AudioLines size={20} /></span>
            <span>CX Platform</span>
          </div>

          <h1 className="login-hero">Live voice support, person to person.</h1>
          <p className="login-lede">
            Customers call from the browser, agents answer from their inbox,
            and every word is captioned and saved.
          </p>

          <ul className="login-features">
            {FEATURES.map(({ icon: Icon, title, text }) => (
              <li key={title}>
                <span className="login-feature-icon"><Icon size={16} /></span>
                <div>
                  <div className="login-feature-title">{title}</div>
                  {text && <div className="login-feature-text">{text}</div>}
                </div>
              </li>
            ))}
          </ul>

          <div className="login-copyright">© CX Platform</div>
        </section>

        {/* ── Form panel ── */}
        <section className="login-form-panel">
          <h2 className="login-title">Sign in</h2>
          <p className="login-subtitle">Welcome back. Choose how you're signing in.</p>

          <div className="login-tabs" role="tablist">
            {Object.entries(ROLES).map(([key, r]) => (
              <button
                key={key}
                type="button"
                role="tab"
                aria-selected={role === key}
                className={`login-tab ${role === key ? 'active' : ''}`}
                onClick={() => switchRole(key)}
              >
                {r.label}
              </button>
            ))}
          </div>

          <p className="login-role-desc">{current.description}</p>

          <form onSubmit={handleSubmit} noValidate>
            <label className="login-label" htmlFor="login-email">Work email</label>
            <input
              id="login-email"
              className="login-input"
              type="email"
              autoComplete="username"
              placeholder="you@company.com"
              value={email}
              onChange={e => setEmail(e.target.value)}
            />

            <label className="login-label" htmlFor="login-password">Password</label>
            <div className="login-password-wrap">
              <input
                id="login-password"
                className="login-input"
                type={showPassword ? 'text' : 'password'}
                autoComplete="current-password"
                placeholder="Enter your password"
                value={password}
                onChange={e => setPassword(e.target.value)}
              />
              <button type="button" className="login-show-btn" onClick={() => setShowPassword(s => !s)}>
                {showPassword ? 'Hide' : 'Show'}
              </button>
            </div>

            <div className="login-row">
              <label className="login-checkbox">
                <input type="checkbox" checked={remember} onChange={e => setRemember(e.target.checked)} />
                Keep me signed in
              </label>
              <button
                type="button"
                className="login-link"
                onClick={() => { setError(''); setNotice(role === 'admin'
                  ? 'Contact another administrator to reset your password.'
                  : 'Ask your administrator to reset your password from the Users page.'); }}
              >
                Forgot password?
              </button>
            </div>

            {error && <div className="login-alert error" role="alert">{error}</div>}
            {notice && <div className="login-alert info">{notice}</div>}

            <button type="submit" className="login-submit" disabled={submitting}>
              {submitting ? 'Signing in…' : <>Sign in as {current.label.toLowerCase()} <ArrowRight size={18} /></>}
            </button>
          </form>

          <div className="login-demo">
            <div>
              <div className="login-demo-label">Demo {current.label} account</div>
              <div className="login-demo-creds">{current.demo.email} · {current.demo.password}</div>
            </div>
            <button type="button" className="login-demo-btn" onClick={fillDemo}>Fill in</button>
          </div>
        </section>
      </div>
    </div>
  );
}
