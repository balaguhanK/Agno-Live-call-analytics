import React, { useEffect, useRef, useState } from 'react';
import { SourceLinks } from './TranscriptTimeline';

const QUICK_ACTIONS = [
  { label: 'Summarize current call', action: 'summary', prompt: 'Summarize the current call.' },
  { label: 'Identify current topic', action: 'topic', prompt: 'What is the current topic of the call?' },
  { label: 'Generate email', action: 'email', prompt: 'Generate a follow-up email for the customer.' },
];

const WELCOME = 'LCA Agent Assistant is ready. Ask knowledge base questions or request a summary of the current call.';
const NOT_CONNECTED = "The assistant isn't connected yet, so it can't answer. This will work once the assistant service is added.";

// How long "Copied" shows after copying an email
const COPIED_MS = 2000;

const SpeechRecognition = typeof window !== 'undefined'
  ? window.SpeechRecognition || window.webkitSpeechRecognition
  : undefined;

// Until a backend is plugged in, every request gets the same reply
const notConnected = async () => NOT_CONNECTED;

/**
 * Agent Assist Bot: a chat panel for the agent during a call. It only
 * answers the agent: the three quick actions and what they type or say.
 * Knowledge-base suggestions for what the customer says appear in the call
 * transcript, not here.
 *
 * onAsk(question, { callId, action }) → Promise<string | { answer, sources }>
 * answers it; action is 'ask' for typed questions or a quick action id.
 * Defaults to a "not connected" reply.
 */
export default function AgentAssistBot({ callId, onAsk = notConnected }) {
  const [messages, setMessages] = useState([]);
  const [input, setInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [listening, setListening] = useState(false);
  const listRef = useRef(null);
  const recognitionRef = useRef(null);
  const nextId = useRef(1);

  useEffect(() => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight, behavior: 'smooth' });
  }, [messages, busy]);

  useEffect(() => () => recognitionRef.current?.abort(), []);

  const add = (from, text, sources = [], model = '', action = '') => setMessages(m => [...m, { id: nextId.current++, from, text, sources, model, action }]);

  const [copiedId, setCopiedId] = useState(null);
  const copy = async (m) => {
    try {
      await navigator.clipboard.writeText(m.text);
      setCopiedId(m.id);
      setTimeout(() => setCopiedId(id => (id === m.id ? null : id)), COPIED_MS);
    } catch {
      // Clipboard blocked (e.g. not https): the text can still be selected
    }
  };

  const ask = async (question, action = 'ask') => {
    const q = question.trim();
    if (!q || busy) return;
    add('agent', q);
    setInput('');
    setBusy(true);
    try {
      const reply = await onAsk(q, { callId, action });
      if (typeof reply === 'string') add('bot', reply, [], '', action);
      else add('bot', reply.answer, reply.sources || [], reply.model || '', action);
    } catch (err) {
      add('bot', `Sorry, something went wrong: ${err.message}`);
    } finally {
      setBusy(false);
    }
  };

  // Dictate a question with the browser's speech recognition
  const toggleMic = () => {
    if (listening) {
      recognitionRef.current?.stop();
      return;
    }
    const rec = new SpeechRecognition();
    rec.lang = 'en-IN';
    rec.interimResults = true;
    rec.onresult = (e) => setInput([...e.results].map(r => r[0].transcript).join(' '));
    rec.onend = () => setListening(false);
    rec.onerror = () => setListening(false);
    recognitionRef.current = rec;
    setListening(true);
    rec.start();
  };

  return (
    <aside className="assist-bot" aria-label="Agent Assist Bot">
      <div className="assist-bot-header">
        <span className="assist-bot-title">Agent Assist Bot</span>
        <a className="info-link" href="#" onClick={e => e.preventDefault()}>Info</a>
      </div>

      <div className="assist-bot-body" ref={listRef} aria-live="polite">
        <div className="assist-bot-msg bot welcome">{WELCOME}</div>

        <div className="assist-bot-actions">
          {QUICK_ACTIONS.map(a => (
            <button key={a.label} type="button" className="assist-bot-action" onClick={() => ask(a.prompt, a.action)} disabled={busy}>
              {a.label}
            </button>
          ))}
        </div>

        {messages.map(m => (
          <div key={m.id} className={`assist-bot-msg ${m.from}`}>
            {m.text}
            {m.sources.length > 0 && <SourceLinks sources={m.sources} />}
            {m.action === 'email' && (
              <button type="button" className="assist-bot-copy" onClick={() => copy(m)} aria-label="Copy email">
                {copiedId === m.id ? 'Copied' : 'Copy email'}
              </button>
            )}
          </div>
        ))}
        {busy && <div className="assist-bot-msg bot typing" role="status">Thinking…</div>}
      </div>

      <form className="assist-bot-input" onSubmit={e => { e.preventDefault(); ask(input); }}>
        <input
          value={input}
          onChange={e => setInput(e.target.value)}
          placeholder="Type here or click on the mic"
          aria-label="Ask the assistant"
          disabled={busy}
        />
        {SpeechRecognition && (
          <button
            type="button"
            className={`assist-bot-mic ${listening ? 'listening' : ''}`}
            onClick={toggleMic}
            aria-label={listening ? 'Stop dictating' : 'Dictate a question'}
            aria-pressed={listening}
            disabled={busy}
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
              <path d="M12 14a3 3 0 0 0 3-3V5a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3zm5-3a5 5 0 0 1-10 0H5a7 7 0 0 0 6 6.92V21h2v-3.08A7 7 0 0 0 19 11h-2z" />
            </svg>
          </button>
        )}
      </form>
    </aside>
  );
}
