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

export function getDataCollection(
  id: string
): Promise<{dataCollection: DataCollection; resources: Resource[]; fields: Field[]}> {
  return req(`/api/data-collections/${id}`);
}

export function suggest(id: string, opts?: {deep?: boolean}): Promise<SuggestResult> {
  return req(`/api/data-collections/${id}/suggest${opts?.deep ? '?mode=deep' : ''}`, {
    method: 'POST',
  });
}

/**
 * suggestStream runs generation over Server-Sent Events, invoking `onProgress`
 * for each pipeline step and resolving with the final suggestions + profile.
 * The GET endpoint streams `progress` events, then a `result` (or `fail`) event.
 * Pass `{deep: true}` for the extensive (agentic) profiling flow.
 */
export function suggestStream(
  id: string,
  onProgress: (p: Progress) => void,
  opts?: {deep?: boolean}
): Promise<SuggestResult> {
  return new Promise((resolve, reject) => {
    const es = new EventSource(
      `/api/data-collections/${id}/suggest${opts?.deep ? '?mode=deep' : ''}`
    );
    let settled = false;

    es.addEventListener('progress', (e) => {
      try {
        onProgress(JSON.parse((e as MessageEvent).data));
      } catch {
        /* ignore malformed progress frames */
      }
    });
    es.addEventListener('result', (e) => {
      settled = true;
      es.close();
      resolve(JSON.parse((e as MessageEvent).data));
    });
    es.addEventListener('fail', (e) => {
      settled = true;
      es.close();
      let msg = 'generation failed';
      try {
        msg = JSON.parse((e as MessageEvent).data).message ?? msg;
      } catch {
        /* keep default */
      }
      reject(new Error(msg));
    });
    // Built-in error = connection dropped (not an app-level `fail` frame).
    es.addEventListener('error', () => {
      if (settled) return;
      settled = true;
      es.close();
      reject(new Error('Lost connection to the server during generation.'));
    });
  });
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
