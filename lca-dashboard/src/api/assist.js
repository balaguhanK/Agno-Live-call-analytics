import { api } from './client';

// Agent Assist Bot. action: 'ask' (knowledge-base question) or a quick
// action over the call: 'summary' | 'topic' | 'email'.
// Resolves to { answer, sources: [{ title, section, file, score }] }.
export const askAssistant = ({ action = 'ask', question = '', callId = '' }) =>
  api('/assist', { method: 'POST', body: { action, question, callId } });

// { ready, documents, passages, model, error }
export const getAssistStatus = () => api('/assist/status');
