# Data Collection Profiler

A custom Verily Workbench app that helps data collection owners fill in catalog
metadata with AI-generated suggestions grounded in the collection's actual data.

## What it does

1. **Pick a data collection.** Lists every data collection you have `OWNER` or
   `WRITER` access to.
2. **Inspect the data.** Reads the collection's BigQuery tables (schema, row
   counts, sample rows) and GCS buckets (object listing, small file previews).
3. **Suggest metadata.** Uses Vertex AI (Gemini) to generate suggestions for
   catalog fields — description, short summary, data snapshot, data dictionary,
   data model, sample use cases, sample SQL queries, references, external
   documentation, geographic coverage, time frame, update frequency, and
   therapeutic / data-modality tags.
4. **Review side-by-side.** Every field shows the **current** stored value next
   to the **suggested** value. You edit freely, accept, or keep the current
   value — nothing is auto-overwritten.
5. **Save.** Approved values are written back to the workspace via the Workspace
   Manager REST API — the built-in `description` via `PATCH`, everything else as
   `terra-dc-*` / `terra-*` workspace properties.

## Architecture

- **Backend (Go).** Serves the SPA and a small JSON API under `/api/*`.
  - `wsm.go` — Workspace Manager REST client. Auth token from
    `wb auth print-access-token`; base URL from `wb status --format=json`.
  - `inspect.go` — BigQuery + GCS profiling via Google client libraries (ADC).
  - `generate.go` — Vertex AI Gemini generation with a JSON response schema.
  - `fields.go` — the catalog field definitions and tag/enum option lists.
  - `main.go` — HTTP server and handlers.
- **Frontend (React + MUI + Vite).** DC picker, data-asset summary, and the
  side-by-side review/edit/save UI.

The Go server calls the `wb` CLI lazily (per request), so it can start before
the Workbench post-startup hook finishes installing and authenticating `wb`.
BigQuery, GCS, and Vertex AI use Application Default Credentials (the VM's pet
service account) via the metadata server.

## API

| Method + path | Purpose |
| --- | --- |
| `GET /api/data-collections` | List writable data collections |
| `GET /api/data-collections/{id}` | DC details + resources + field catalog |
| `POST /api/data-collections/{id}/suggest` | Inspect data + generate suggestions |
| `POST /api/data-collections/{id}/save` | Persist approved field values |
| `GET /api/health` | Health check |

## Local development

```bash
# Backend (needs a configured `wb` CLI and ADC on the machine)
cd backend && go run .

# Frontend (proxies /api to :8080)
cd frontend && npm install && npm run dev
```

## Deployment

Deployed as a Workbench custom app. The container runs the Go server on
`0.0.0.0:8080`; the Workbench `post-startup.sh` hook installs and authenticates
the `wb` CLI inside the container.
