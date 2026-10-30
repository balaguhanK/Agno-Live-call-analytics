import React, { useEffect, useState } from 'react';
import { listUsers, createUser, setUserActive, resetPassword, deleteUser } from '../auth/authService';

const EMPTY_FORM = { name: '', email: '', agentName: '', password: '', role: 'agent' };

function formatDate(iso) {
  return iso ? iso.replace('T', ' ').slice(0, 16) : '—';
}

export default function UsersPage({ currentUser }) {
  const [users, setUsers] = useState([]);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState(EMPTY_FORM);
  const [resetFor, setResetFor] = useState(null);
  const [newPassword, setNewPassword] = useState('');
  const [message, setMessage] = useState(null);

  const refresh = async () => setUsers(await listUsers());

  useEffect(() => {
    listUsers().then(setUsers).catch(err => setMessage({ type: 'error', text: err.message }));
  }, []);

  // Wrap each action so errors and successes show in one place
  const run = async (action, success) => {
    try {
      await action();
      await refresh();
      setMessage({ type: 'success', text: success });
      return true;
    } catch (err) {
      setMessage({ type: 'error', text: err.message });
      return false;
    }
  };

  const handleCreate = async (e) => {
    e.preventDefault();
    if (await run(() => createUser(form), `Created ${form.email.trim().toLowerCase()}.`)) {
      setForm(EMPTY_FORM);
      setShowForm(false);
    }
  };

  const handleReset = async (e) => {
    e.preventDefault();
    if (await run(() => resetPassword(resetFor, newPassword), `Password reset for ${resetFor}.`)) {
      setResetFor(null);
      setNewPassword('');
    }
  };

  const handleDelete = (email) => {
    if (window.confirm(`Delete ${email}? This cannot be undone.`)) {
      run(() => deleteUser(email), `Deleted ${email}.`);
    }
  };

  const update = (field) => (e) => setForm(f => ({ ...f, [field]: e.target.value }));

  return (
    <>
      <div className="breadcrumb">
        <a>Call Analytics</a>
        <span className="breadcrumb-sep">›</span>
        <span>Users</span>
      </div>

      <div style={{ margin: '0 24px 16px' }}>
        {message && (
          <div className={`users-message ${message.type}`}>
            {message.text}
            <button onClick={() => setMessage(null)}>✕</button>
          </div>
        )}

        {showForm && (
          <form className="cs-container users-form" style={{ margin: '0 0 16px' }} onSubmit={handleCreate}>
            <div className="cs-container-header">
              <span className="cs-container-title">Add user</span>
            </div>
            <div className="users-form-grid">
              <label>Full name<input value={form.name} onChange={update('name')} placeholder="Jane Doe" /></label>
              <label>Work email<input type="email" value={form.email} onChange={update('email')} placeholder="jane@cxplatform.com" /></label>
              <label>Role
                <select value={form.role} onChange={update('role')}>
                  <option value="agent">Agent</option>
                  <option value="admin">Admin</option>
                </select>
              </label>
              {form.role === 'agent' && (
                <label>Agent ID<input value={form.agentName} onChange={update('agentName')} placeholder="JaneD" /></label>
              )}
              <label>Temporary password<input type="password" value={form.password} onChange={update('password')} placeholder="At least 6 characters" /></label>
            </div>
            <div className="users-form-actions">
              <button type="button" className="btn btn-normal" onClick={() => { setShowForm(false); setForm(EMPTY_FORM); }}>Cancel</button>
              <button type="submit" className="btn btn-primary">Create user</button>
            </div>
          </form>
        )}

        <div className="cs-table-wrapper">
          <div className="users-header">
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
              <span style={{ fontSize: '18px', fontWeight: '700' }}>Users</span>
              <span style={{ fontSize: '14px', color: '#687078' }}>({users.length})</span>
            </div>
            {!showForm && (
              <button className="btn btn-primary" onClick={() => { setShowForm(true); setMessage(null); }}>Add user</button>
            )}
          </div>

          <table className="cs-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Email</th>
                <th>Role</th>
                <th>Agent ID</th>
                <th>Calls</th>
                <th>Status</th>
                <th>Last sign-in</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map(u => {
                const isSelf = u.email === currentUser.email;
                return (
                  <React.Fragment key={u.email}>
                    <tr style={{ cursor: 'default' }}>
                      <td>{u.name}{isSelf && <span className="users-you">You</span>}</td>
                      <td>{u.email}</td>
                      <td><span className={`category-tag ${u.role === 'admin' ? 'red' : 'blue'}`}>{u.role === 'admin' ? 'Admin' : 'Agent'}</span></td>
                      <td>{u.agentName || '—'}</td>
                      <td>{u.agentName ? u.callCount : '—'}</td>
                      <td>
                        <span className={`users-status ${u.active ? 'active' : 'disabled'}`}>
                          {u.active ? 'Active' : 'Disabled'}
                        </span>
                      </td>
                      <td style={{ whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>{formatDate(u.lastSignIn)}</td>
                      <td>
                        <div className="users-actions">
                          <button className="btn-link" onClick={() => { setResetFor(u.email); setNewPassword(''); setMessage(null); }}>Reset password</button>
                          {!isSelf && (
                            <>
                              <button
                                className="btn-link"
                                onClick={() => run(() => setUserActive(u.email, !u.active), `${u.active ? 'Disabled' : 'Enabled'} ${u.email}.`)}
                              >
                                {u.active ? 'Disable' : 'Enable'}
                              </button>
                              <button className="btn-link danger" onClick={() => handleDelete(u.email)}>Delete</button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                    {resetFor === u.email && (
                      <tr style={{ cursor: 'default', background: '#f9fafa' }}>
                        <td colSpan={8}>
                          <form className="users-reset" onSubmit={handleReset}>
                            <span>New password for <strong>{u.email}</strong>:</span>
                            <input type="password" autoFocus value={newPassword} onChange={e => setNewPassword(e.target.value)} placeholder="At least 6 characters" />
                            <button type="submit" className="btn btn-primary">Save</button>
                            <button type="button" className="btn btn-normal" onClick={() => setResetFor(null)}>Cancel</button>
                          </form>
                        </td>
                      </tr>
                    )}
                  </React.Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>
    </>
  );
}
