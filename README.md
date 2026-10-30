# Agno Live Call Analytics

Live call analytics with an agent assistant for a contact centre. While a call is going on it shows a live
transcript with sentiment, the issues the caller raises, call categories, and short, formal suggested replies
for the agent. After the call it writes a summary with actions and shows a post-call analytics (PCA) page.

## What it does

- **Live transcript** from Deepgram (nova-3, Indian English by default), with a face for each line's sentiment
  (negative, neutral, positive), live translation, and an "Issue Detected" mark on lines where the caller reports a problem.
- **Agent assistant** inside the transcript: one bold, formal "Suggested response" line for each customer line.
  Steps and a source link are added only when the customer asks about a policy, steps or rules. Policy answers
  come from the knowledge base in `lca-backend/knowledge/`.
- **Issues vs inquiries**: problems are listed as issues; requests for information (for example "I want to know
  about the personal loan") are not.
- **Post-call**: summary, details and actions, the issues, entities (people, products, amounts, dates, references),
  categories, sentiment and talk time, and a highlighted transcript on the PCA page.
- **Softphone**: customers dial a queue number through FreeSWITCH and the call rings in the agent's browser.
- Text is redacted for personal and card data before it is stored or sent to a model.

## Layout

| Folder | What it is |
|---|---|
| `lca-backend/` | Go server: API, live streaming, analysis, knowledge base search, FreeSWITCH and SIPREC helpers |
| `lca-dashboard/` | React + Vite dashboard (live calls, Phone, Stream Audio, and the PCA page at `#/pca`) |
| `lca-backend/knowledge/` | **Sample** policy documents for the assistant. Replace them with your own policies. |

## Run it locally

You need Go, Node.js, and [Ollama](https://ollama.com) with the two models used:

```
ollama pull gemma2:2b
ollama pull embeddinggemma
```

1. Copy `lca-dashboard/.env.example` to `lca-dashboard/.env` and fill it in. The file is gitignored; never commit it.
   Without `DEEPGRAM_API_KEY` the dashboard falls back to browser speech recognition. Without `DATABASE_URL`
   and `REDIS_URL` the server keeps users and calls in memory, so they reset on restart.
2. Start the backend from `lca-backend/`: `go run ./cmd/server` (it reads `../lca-dashboard/.env`, or set `LCA_ENV_FILE`).
3. Start the dashboard from `lca-dashboard/`: `npm install`, then `npm run dev`.
4. Open the address Vite prints and sign in.

The server seeds demo users with default passwords. Change them before using this anywhere real.

## Tests

- Backend, from `lca-backend/`: `go vet ./...` and `go test ./...`
- Dashboard, from `lca-dashboard/`: `npm run lint` and `npm run build`

## Notes

- The suggested replies, summaries and translations come from a small local model (`gemma2:2b`) running on the CPU.
  Replies for topics the knowledge base covers are instant; other replies can take several seconds.
  Translation is reliable for languages like French and Spanish and only approximate for Hindi and Tamil.
- Keep personal data out of the repository: no recordings, no `.env` files, no real customer transcripts.
