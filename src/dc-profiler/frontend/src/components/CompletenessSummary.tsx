import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import Stack from '@mui/material/Stack';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import type {Suggestion} from '../api/types';
import {fieldStatus, isEmptyEdit, originalEdit, type EditValue} from './fieldStatus';

interface Props {
  suggestions: Suggestion[];
  edits: Record<string, EditValue>;
}

/**
 * CompletenessSummary shows how many catalog fields are filled now versus after
 * the pending edits are saved, plus how many fields are newly filled or changed.
 * It is a review aid only — nothing here is written to the data collection.
 */
export function CompletenessSummary({suggestions, edits}: Props) {
  const total = suggestions.length;
  let filledNow = 0;
  let filledAfter = 0;
  let added = 0;
  let changed = 0;

  for (const s of suggestions) {
    const v = edits[s.key] ?? {};
    if (!isEmptyEdit(originalEdit(s), s.kind)) filledNow++;
    if (!isEmptyEdit(v, s.kind)) filledAfter++;
    const st = fieldStatus(s, v);
    if (st === 'new') added++;
    else if (st === 'changed') changed++;
  }

  const unchanged = total - added - changed;

  // The bar shows ONLY the new/changed/unchanged breakdown — every field is
  // exactly one of these, so the segments sum to the full width and match the
  // chips below one-to-one. Completeness ("filled now → after") is a separate
  // metric shown as text, deliberately not conflated with the bar.
  const segments = [
    {key: 'new', count: added, color: 'success.main'},
    {key: 'changed', count: changed, color: 'warning.main'},
    {key: 'unchanged', count: unchanged, color: 'grey.400'},
  ].filter((s) => s.count > 0);

  return (
    <Box
      sx={{
        mb: 3,
        p: 2,
        border: '1px solid',
        borderColor: 'divider',
        borderRadius: 1,
        bgcolor: 'grey.50',
      }}
    >
      <Box sx={{display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', mb: 1}}>
        <Typography variant="subtitle2" fontWeight={700}>
          Catalog completeness
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {filledNow} of {total} filled now → <strong>{filledAfter} of {total}</strong> after saving
        </Typography>
      </Box>

      <Box
        sx={{
          display: 'flex',
          height: 8,
          borderRadius: 1,
          overflow: 'hidden',
          bgcolor: 'grey.200', // track = fields still empty after saving
          mb: 1.5,
        }}
      >
        {segments.map((seg) => (
          <Tooltip key={seg.key} title={`${seg.count} ${seg.key}`}>
            <Box sx={{width: `${(seg.count / total) * 100}%`, bgcolor: seg.color}} />
          </Tooltip>
        ))}
      </Box>

      <Stack direction="row" spacing={1}>
        <Chip label={`${added} new`} size="small" color="success" variant={added ? 'filled' : 'outlined'} />
        <Chip
          label={`${changed} changed`}
          size="small"
          color="warning"
          variant={changed ? 'filled' : 'outlined'}
        />
        <Chip label={`${total - added - changed} unchanged`} size="small" variant="outlined" />
      </Stack>
    </Box>
  );
}
