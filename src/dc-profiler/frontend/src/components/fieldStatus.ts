import type {Suggestion} from '../api/types';

// Source records where the current "Your value" came from, so the toggle
// buttons can highlight the active source. It goes to "custom" as soon as the
// user edits the value themselves.
export type Source = 'original' | 'ai' | 'custom';

// EditValue is the user-editable state for one field: free text in `value`, or
// a set of slugs in `tags` for tag/enum fields.
export interface EditValue {
  value?: string;
  tags?: string[];
  source?: Source;
}

// FieldStatus describes the user's value relative to what is currently stored.
export type FieldStatus = 'new' | 'changed' | 'unchanged';

export function labelFor(s: Suggestion, value: string): string {
  return s.options?.find((o) => o.value === value)?.label ?? value;
}

// parseCurrentTags recovers the slug list from a suggestion's current display
// value (comma-separated slugs, as the backend renders JSON arrays).
export function parseCurrentTags(s: Suggestion): string[] {
  if (!s.current) return [];
  const allowed = new Set((s.options ?? []).map((o) => o.value));
  return s.current
    .split(',')
    .map((v) => v.trim())
    .filter((v) => allowed.has(v));
}

// originalEdit is the EditValue representing the current stored value.
export function originalEdit(s: Suggestion): EditValue {
  if (s.kind === 'tags' || s.kind === 'enum') return {tags: parseCurrentTags(s)};
  return {value: s.current};
}

// aiEdit is the EditValue representing the AI-suggested value.
export function aiEdit(s: Suggestion): EditValue {
  if (s.kind === 'tags' || s.kind === 'enum') return {tags: s.tags ?? []};
  return {value: s.suggested};
}

// editEquals compares two EditValues for a field kind (order-insensitive for tags).
export function editEquals(a: EditValue, b: EditValue, kind: string): boolean {
  if (kind === 'tags' || kind === 'enum') {
    const sa = [...(a.tags ?? [])].sort();
    const sb = [...(b.tags ?? [])].sort();
    return sa.length === sb.length && sa.every((v, i) => v === sb[i]);
  }
  return (a.value ?? '').trim() === (b.value ?? '').trim();
}

// isEmptyEdit reports whether an EditValue holds no content.
export function isEmptyEdit(v: EditValue, kind: string): boolean {
  if (kind === 'tags' || kind === 'enum') return (v.tags ?? []).length === 0;
  return !(v.value ?? '').trim();
}

// fieldStatus classifies the user's value relative to the current stored value:
// "new" (was empty, now filled), "changed" (differs from stored), or
// "unchanged".
export function fieldStatus(s: Suggestion, v: EditValue): FieldStatus {
  const orig = originalEdit(s);
  if (editEquals(v, orig, s.kind)) return 'unchanged';
  if (isEmptyEdit(orig, s.kind)) return 'new';
  return 'changed';
}
