import {useRef, useState} from 'react';
import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Chip from '@mui/material/Chip';
import FormControl from '@mui/material/FormControl';
import InputLabel from '@mui/material/InputLabel';
import MenuItem from '@mui/material/MenuItem';
import OutlinedInput from '@mui/material/OutlinedInput';
import Select from '@mui/material/Select';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import ToggleButton from '@mui/material/ToggleButton';
import Typography from '@mui/material/Typography';
import AutoAwesomeIcon from '@mui/icons-material/AutoAwesome';
import HistoryIcon from '@mui/icons-material/History';
import type {Suggestion} from '../api/types';
import {MarkdownView, MarkdownEditor} from './Markdown';
import {
  aiEdit,
  editEquals,
  fieldStatus,
  labelFor,
  originalEdit,
  type EditValue,
  type Source,
} from './fieldStatus';

export type {EditValue, Source} from './fieldStatus';

interface Props {
  suggestion: Suggestion;
  value: EditValue;
  onChange: (v: EditValue) => void;
}

/**
 * Renders one catalog field for review. The top row shows the current stored
 * value and the AI suggestion side-by-side, both rendered read-only. The bottom
 * row is the user's editable final value: for markdown fields this is a single
 * WYSIWYG editor (which doubles as its own preview); simpler fields use a text
 * box or a select. Two toggle buttons pull in the original or AI value, and
 * light up while the value matches that source.
 */
export function SuggestionRow({suggestion, value, onChange}: Props) {
  const isMarkdown = suggestion.kind === 'text';

  const original = originalEdit(suggestion);
  const ai = aiEdit(suggestion);

  // For markdown fields the WYSIWYG editor normalizes content, so we track the
  // active source explicitly. Other kinds compare values directly, which also
  // re-lights a button if the user manually types a value back to a source.
  const matchesOriginal = isMarkdown
    ? value.source === 'original'
    : editEquals(value, original, suggestion.kind);
  const matchesAI = isMarkdown
    ? value.source === 'ai'
    : editEquals(value, ai, suggestion.kind);

  // editorKey forces the MDXEditor to remount (and thus adopt new markdown)
  // when a toggle button loads a different source.
  const [editorKey, setEditorKey] = useState(0);
  // lastNormalizedKey tracks which mount we've already absorbed the initial
  // (normalization) onChange for, so we don't misread it as a user edit.
  const lastNormalizedKey = useRef(-1);

  const loadSource = (edit: EditValue, source: Source) => {
    onChange({...edit, source});
    setEditorKey((k) => k + 1);
  };

  const handleEditorChange = (md: string) => {
    if (lastNormalizedKey.current !== editorKey) {
      // First onChange after this (re)mount is MDXEditor normalizing the loaded
      // markdown — keep the source it was loaded with, just store the text.
      lastNormalizedKey.current = editorKey;
      onChange({...value, value: md});
      return;
    }
    onChange({value: md, source: 'custom'});
  };

  const onOriginal = () =>
    isMarkdown ? loadSource(original, 'original') : onChange({...original, source: 'original'});
  const onAI = () => (isMarkdown ? loadSource(ai, 'ai') : onChange({...ai, source: 'ai'}));

  const status = fieldStatus(suggestion, value);

  return (
    <Card variant="outlined">
      <CardContent>
        <Box sx={{display: 'flex', alignItems: 'center', gap: 1, mb: 1.5}}>
          <Typography variant="subtitle1" fontWeight={600}>
            {suggestion.label}
          </Typography>
          <StatusChip status={status} />
        </Box>

        {/* Top row: current and AI values, rendered read-only. */}
        <Box
          sx={{
            display: 'grid',
            // minmax(0, 1fr) forces a true 50/50 split; plain "1fr" is
            // minmax(auto, 1fr), which lets a wide table or code block in one
            // column stretch that track and shrink the other.
            gridTemplateColumns: {xs: '1fr', sm: 'minmax(0, 1fr) minmax(0, 1fr)'},
            gap: 2,
            alignItems: 'start',
            mb: 2,
          }}
        >
          {/* minWidth:0 lets each column shrink to its 50% track; without it the
              grid item defaults to min-width:auto and wide content (e.g. the data
              dictionary table) overflows and pushes the other column out of view. */}
          <Box sx={{minWidth: 0}}>
            <ColumnHeader label="Current" />
            <ReadOnlyValue suggestion={suggestion} edit={original} />
          </Box>
          <Box sx={{minWidth: 0}}>
            <ColumnHeader label="AI suggested" />
            <ReadOnlyValue suggestion={suggestion} edit={ai} />
          </Box>
        </Box>

        {/* Bottom row: the user's final, editable value (full width). The
            source toggle buttons sit inline with the header. */}
        <Box sx={{display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 0.75}}>
          <Typography variant="subtitle2" fontWeight={700} color="text.primary">
            Final value
          </Typography>
          <Stack direction="row" spacing={1}>
            <ToggleButton
              value="original"
              size="small"
              selected={matchesOriginal}
              onChange={onOriginal}
              sx={{textTransform: 'none', py: 0.25}}
            >
              <HistoryIcon fontSize="small" sx={{mr: 0.5}} />
              Original
            </ToggleButton>
            <ToggleButton
              value="ai"
              size="small"
              selected={matchesAI}
              onChange={onAI}
              sx={{textTransform: 'none', py: 0.25}}
            >
              <AutoAwesomeIcon fontSize="small" sx={{mr: 0.5}} />
              AI suggested
            </ToggleButton>
          </Stack>
        </Box>

        {isMarkdown ? (
          <MarkdownEditor key={editorKey} markdown={value.value ?? ''} onChange={handleEditorChange} />
        ) : (
          <SimpleEditor suggestion={suggestion} value={value} onChange={onChange} />
        )}
      </CardContent>
    </Card>
  );
}

function ColumnHeader({label}: {label: string}) {
  return (
    <Typography
      variant="caption"
      color="text.secondary"
      sx={{display: 'block', mb: 0.5, fontWeight: 600}}
    >
      {label}
    </Typography>
  );
}

// StatusChip shows whether the field's value will be added, changed, or left as
// is relative to what is currently stored on the data collection.
function StatusChip({status}: {status: 'new' | 'changed' | 'unchanged'}) {
  if (status === 'unchanged') {
    return <Chip label="Unchanged" size="small" variant="outlined" />;
  }
  if (status === 'new') {
    return <Chip label="New" size="small" color="success" />;
  }
  return <Chip label="Changed" size="small" color="warning" />;
}

// ReadOnlyValue renders a source value (current or AI) for the top two columns:
// rendered markdown for text fields, chips for tag/enum, plain text otherwise.
// These are reference-only, so they sit on a subdued grey background.
function ReadOnlyValue({suggestion, edit}: {suggestion: Suggestion; edit: EditValue}) {
  const isTagLike = suggestion.kind === 'tags' || suggestion.kind === 'enum';
  const isMarkdown = suggestion.kind === 'text';
  const empty = isTagLike ? (edit.tags ?? []).length === 0 : !edit.value;

  return (
    <Box
      sx={{
        p: 1.5,
        bgcolor: 'grey.100',
        border: '1px solid',
        borderColor: 'divider',
        borderRadius: 1,
        minHeight: 56,
        maxHeight: 280,
        overflow: 'auto',
        whiteSpace: isMarkdown ? 'normal' : 'pre-wrap',
        fontSize: 14,
        color: 'text.secondary',
      }}
    >
      {empty ? (
        <Typography component="span" color="text.disabled" fontSize={14}>
          (empty)
        </Typography>
      ) : isTagLike ? (
        <Box sx={{display: 'flex', flexWrap: 'wrap', gap: 0.5}}>
          {(edit.tags ?? []).map((v) => (
            <Chip key={v} label={labelFor(suggestion, v)} size="small" />
          ))}
        </Box>
      ) : isMarkdown ? (
        <MarkdownView markdown={edit.value ?? ''} />
      ) : (
        edit.value
      )}
    </Box>
  );
}

// SimpleEditor is the "Your value" control for non-markdown fields: a multi- or
// single-line text box for plaintext/shorttext, or a select for tags/enum.
function SimpleEditor({
  suggestion: s,
  value,
  onChange,
}: {
  suggestion: Suggestion;
  value: EditValue;
  onChange: (v: EditValue) => void;
}) {
  if (s.kind === 'tags') {
    const selected = value.tags ?? [];
    return (
      <FormControl fullWidth size="small">
        <InputLabel>{s.label}</InputLabel>
        <Select
          multiple
          value={selected}
          onChange={(e) =>
            onChange({
              tags: typeof e.target.value === 'string' ? e.target.value.split(',') : e.target.value,
              source: 'custom',
            })
          }
          input={<OutlinedInput label={s.label} />}
          renderValue={(sel) => (
            <Box sx={{display: 'flex', flexWrap: 'wrap', gap: 0.5}}>
              {(sel as string[]).map((v) => (
                <Chip key={v} label={labelFor(s, v)} size="small" />
              ))}
            </Box>
          )}
        >
          {(s.options ?? []).map((o) => (
            <MenuItem key={o.value} value={o.value}>
              {o.label}
            </MenuItem>
          ))}
        </Select>
      </FormControl>
    );
  }

  if (s.kind === 'enum') {
    const current = (value.tags ?? [])[0] ?? '';
    return (
      <FormControl fullWidth size="small">
        <InputLabel>{s.label}</InputLabel>
        <Select
          value={current}
          label={s.label}
          onChange={(e) => onChange({tags: e.target.value ? [e.target.value] : [], source: 'custom'})}
        >
          <MenuItem value="">
            <em>(none)</em>
          </MenuItem>
          {(s.options ?? []).map((o) => (
            <MenuItem key={o.value} value={o.value}>
              {o.label}
            </MenuItem>
          ))}
        </Select>
      </FormControl>
    );
  }

  // plaintext (multi-line) and shorttext (single-line).
  const multiline = s.kind === 'plaintext';
  const text = value.value ?? '';
  const over = s.maxLen ? text.length > s.maxLen : false;
  return (
    <TextField
      fullWidth
      multiline={multiline}
      minRows={multiline ? 3 : 1}
      maxRows={multiline ? 12 : 1}
      value={text}
      onChange={(e) => onChange({value: e.target.value, source: 'custom'})}
      error={over}
      helperText={s.maxLen ? `${text.length}/${s.maxLen}${over ? ' — too long' : ''}` : undefined}
    />
  );
}

