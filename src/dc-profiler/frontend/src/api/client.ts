import type {
  DataCollection,
  DataProfile,
  DeepProfile,
  Field,
  Investigation,
  Progress,
  Resource,
  SaveFieldValue,
  Suggestion,
  Version,
} from './types';

// SuggestResult is the payload returned by both the light and deep flows; the
// deep flow additionally includes the statistical profile and investigation.
export interface SuggestResult {
  suggestions: Suggestion[];
  profile: DataProfile;
  deepProfile?: DeepProfile;
  investigation?: Investigation;
}

async function req<T>(url: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(url, {
    headers: {'Content-Type': 'application/json'},
    ...init,
  });
  if (!resp.ok) {
    let msg = `${resp.status} ${resp.statusText}`;
    try {
      const body = await resp.json();
      if (body?.error) msg = body.error;
    } catch {
      // ignore parse errors, use status text
    }
    throw new Error(msg);
  }
  return resp.json() as Promise<T>;
}

export function listDataCollections(): Promise<{dataCollections: DataCollection[]}> {
  return req('/api/data-collections');
}

export function getDataCollection(id: string): Promise<{
  dataCollection: DataCollection;
  resources: Resource[];
  fields: Field[];
  versions: Version[];
  recommendedVersionId: string;
}> {
  return req(`/api/data-collections/${id}`);
}

// JobState mirrors the backend's job snapshot returned by the poll endpoint.
interface JobState {
  status: 'running' | 'done' | 'error';
  steps: Progress[];
  result?: SuggestResult;
  error?: string;
}

const POLL_INTERVAL_MS = 1500;
// Transient poll failures (a blip, a proxy hiccup) are tolerated this many times
// in a row before we give up — the generation itself keeps running server-side.
const MAX_POLL_ERRORS = 5;

function delay(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

/**
 * runSuggest starts a generation job, then polls it to completion, invoking
 * `onProgress` for each new pipeline step and resolving with the final
 * suggestions + profile. Every request is short, so no single request can hit
 * the mesh proxy's route timeout during long (deep) runs. Pass `{deep: true}`
 * for the extensive (agentic) profiling flow, and `{versionId}` to scope
 * profiling to a single DC version's resources.
 */
export async function runSuggest(
  id: string,
  onProgress: (p: Progress) => void,
  opts?: {deep?: boolean; versionId?: string}
): Promise<SuggestResult> {
  const params = new URLSearchParams();
  if (opts?.deep) params.set('mode', 'deep');
  if (opts?.versionId) params.set('versionId', opts.versionId);
  const q = params.toString();
  const {jobId} = await req<{jobId: string}>(
    `/api/data-collections/${id}/suggest${q ? `?${q}` : ''}`,
    {method: 'POST'}
  );

  let delivered = 0;
  let errors = 0;
  for (;;) {
    let state: JobState;
    try {
      state = await req<JobState>(`/api/data-collections/${id}/suggest/${jobId}`);
      errors = 0;
    } catch (e) {
      if (++errors >= MAX_POLL_ERRORS) throw e;
      await delay(POLL_INTERVAL_MS);
      continue;
    }

    for (; delivered < state.steps.length; delivered++) {
      onProgress(state.steps[delivered]);
    }
    if (state.status === 'done') {
      if (!state.result) throw new Error('generation finished without a result.');
      return state.result;
    }
    if (state.status === 'error') {
      throw new Error(state.error || 'generation failed');
    }
    await delay(POLL_INTERVAL_MS);
  }
}

export function save(
  id: string,
  fields: SaveFieldValue[]
): Promise<{saved: string[]}> {
  return req(`/api/data-collections/${id}/save`, {
    method: 'POST',
    body: JSON.stringify({fields}),
  });
}
