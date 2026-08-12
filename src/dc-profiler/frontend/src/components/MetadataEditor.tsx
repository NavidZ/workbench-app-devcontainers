import {useCallback, useEffect, useState} from 'react';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Alert from '@mui/material/Alert';
import CircularProgress from '@mui/material/CircularProgress';
import Divider from '@mui/material/Divider';
import Link from '@mui/material/Link';
import Snackbar from '@mui/material/Snackbar';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import FormControl from '@mui/material/FormControl';
import InputLabel from '@mui/material/InputLabel';
import Select from '@mui/material/Select';
import MenuItem from '@mui/material/MenuItem';
import AutoAwesomeIcon from '@mui/icons-material/AutoAwesome';
import BiotechIcon from '@mui/icons-material/Biotech';
import SaveIcon from '@mui/icons-material/Save';
import type {
  DataCollection,
  DataProfile,
  Progress,
  Resource,
  SaveFieldValue,
  Suggestion,
  Version,
} from '../api/types';
import {getDataCollection, runSuggest, save} from '../api/client';
import {DataProfileSummary} from './DataProfileSummary';
import {LoadingNote} from './LoadingNote';
import {ProgressLog} from './ProgressLog';
import {CompletenessSummary} from './CompletenessSummary';
import {SuggestionRow, type EditValue} from './SuggestionRow';

interface Props {
  dc: DataCollection;
  onBack: () => void;
  // lockedVersionId pins profiling to a specific version and hides the version
  // picker (used by the /dc/:slug/version/:versionId deep-link route).
  lockedVersionId?: string;
}

// NO_VERSION_ID mirrors the backend's synthetic bucket for root-level resources.
const NO_VERSION_ID = '__none__';

// versionLabel renders a version for the picker: name, its state, and how many
// resources it holds.
function versionLabel(v: Version): string {
  const count = `${v.resourceCount} resource${v.resourceCount === 1 ? '' : 's'}`;
  if (v.id === NO_VERSION_ID) {
    return `${v.name} · ${count}`;
  }
  const tags = [v.isDefault ? 'default' : '', v.isPublished ? 'published' : 'draft']
    .filter(Boolean)
    .join(', ');
  return `${v.name} (${tags}) · ${count}`;
}

/**
 * The review workspace for a single data collection: shows its data assets,
 * generates AI suggestions, and renders each catalog field side-by-side
 * (current vs suggested) for the user to edit and selectively save.
 */
export function MetadataEditor({dc, lockedVersionId}: Props) {
  const [resources, setResources] = useState<Resource[]>([]);
  const [versions, setVersions] = useState<Version[]>([]);
  const [selectedVersionId, setSelectedVersionId] = useState('');
  const [loadingDetail, setLoadingDetail] = useState(true);
  const [detailError, setDetailError] = useState<string | null>(null);

  const [generating, setGenerating] = useState(false);
  const [genError, setGenError] = useState<string | null>(null);
  const [progress, setProgress] = useState<Progress[]>([]);
  const [suggestions, setSuggestions] = useState<Suggestion[] | null>(null);
  const [profile, setProfile] = useState<DataProfile | null>(null);
  // deepMode tracks which button is running so we can disable both correctly.
  const [deepMode, setDeepMode] = useState(false);

  // edits maps a field key to the current editable value the user will save.
  const [edits, setEdits] = useState<Record<string, EditValue>>({});
  // runId increments on every completed generation. It is part of each row's
  // React key so the rows remount and the markdown WYSIWYG editors adopt the
  // freshly seeded AI values (they only read their content on mount).
  const [runId, setRunId] = useState(0);
  const [saving, setSaving] = useState(false);
  const [saveMsg, setSaveMsg] = useState<string | null>(null);
  const [saveErr, setSaveErr] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoadingDetail(true);
    getDataCollection(dc.id)
      .then((r) => {
        if (!active) return;
        setResources(r.resources ?? []);
        setVersions(r.versions ?? []);
        setSelectedVersionId(lockedVersionId || r.recommendedVersionId || '');
      })
      .catch((e: Error) => {
        if (active) setDetailError(e.message);
      })
      .finally(() => {
        if (active) setLoadingDetail(false);
      });
    return () => {
      active = false;
    };
  }, [dc.id, lockedVersionId]);

  const handleGenerate = useCallback(
    async (deep: boolean) => {
      setGenerating(true);
      setDeepMode(deep);
      setGenError(null);
      setProgress([]);
      try {
        const r = await runSuggest(
          dc.id,
          (p) => setProgress((prev) => [...prev, p]),
          {deep, versionId: selectedVersionId}
        );
        setSuggestions(r.suggestions);
        setProfile(r.profile);
        // Seed edits with the suggested values so "accept all" is the default,
        // but the user can revert each field to its current value per-row.
        const seed: Record<string, EditValue> = {};
        for (const s of r.suggestions) {
          seed[s.key] =
            s.kind === 'tags' || s.kind === 'enum'
              ? {tags: s.tags ?? [], source: 'ai'}
              : {value: s.suggested, source: 'ai'};
        }
        setEdits(seed);
        // Bump runId so every SuggestionRow remounts and its editor reloads the
        // new AI value (see runId declaration).
        setRunId((n) => n + 1);
      } catch (e) {
        setGenError((e as Error).message);
      } finally {
        setGenerating(false);
      }
    },
    [dc.id, selectedVersionId]
  );

  // Switching versions invalidates the current suggestions (they were generated
  // for a different version's resources), so clear them.
  const handleVersionChange = useCallback((versionId: string) => {
    setSelectedVersionId(versionId);
    setSuggestions(null);
    setProfile(null);
    setProgress([]);
    setGenError(null);
  }, []);

  const setEdit = useCallback((key: string, value: EditValue) => {
    setEdits((prev) => ({...prev, [key]: value}));
  }, []);

  const handleSave = useCallback(async () => {
    if (!suggestions) return;
    setSaving(true);
    setSaveErr(null);
    setSaveMsg(null);
    try {
      const fields: SaveFieldValue[] = suggestions.map((s) => {
        const e = edits[s.key] ?? {};
        return {key: s.key, value: e.value ?? '', tags: e.tags};
      });
      const r = await save(dc.id, fields);
      setSaveMsg(`Saved ${r.saved.length} field${r.saved.length === 1 ? '' : 's'}.`);
    } catch (e) {
      setSaveErr((e as Error).message);
    } finally {
      setSaving(false);
    }
  }, [dc.id, edits, suggestions]);

  const versionResources = resources.filter(
    (r) => (r.versionId ?? '') === selectedVersionId
  );
  const lockedVersion = lockedVersionId
    ? versions.find((v) => v.id === lockedVersionId)
    : undefined;
  const canGenerate = !generating && !loadingDetail && versionResources.length > 0;

  return (
    <Box>
      <Typography variant="h5">
        {dc.webUrl ? (
          <Link href={dc.webUrl} target="_blank" rel="noopener" underline="hover" color="inherit">
            {dc.displayName || dc.userFacingId}
          </Link>
        ) : (
          dc.displayName || dc.userFacingId
        )}
      </Typography>
      <Typography color="text.secondary" gutterBottom>
        {dc.userFacingId}
        {dc.gcpProject ? ` · ${dc.gcpProject}` : ''}
      </Typography>

      {loadingDetail ? (
        <LoadingNote message="Loading this data collection's versions and resources…" />
      ) : detailError ? (
        <Alert severity="error" sx={{my: 2}}>
          Failed to load resources: {detailError}
        </Alert>
      ) : (
        <>
          {lockedVersionId ? (
            lockedVersion ? (
              <Typography variant="subtitle2" color="text.secondary" sx={{my: 2}}>
                Version: {versionLabel(lockedVersion)}
              </Typography>
            ) : (
              <Alert severity="error" sx={{my: 2}}>
                Version "{lockedVersionId}" was not found in this data collection.
              </Alert>
            )
          ) : (
            versions.length > 0 && (
              <FormControl size="small" sx={{minWidth: 320, my: 2}}>
                <InputLabel id="version-select-label">Version to profile</InputLabel>
                <Select
                  labelId="version-select-label"
                  label="Version to profile"
                  value={selectedVersionId}
                  onChange={(e) => handleVersionChange(e.target.value)}
                  disabled={generating}
                >
                  {versions.map((v) => (
                    <MenuItem key={v.id} value={v.id}>
                      {versionLabel(v)}
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
            )
          )}
          <DataProfileSummary resources={versionResources} profile={profile} />
        </>
      )}

      <Box sx={{my: 3}}>
        <Stack direction="row" spacing={2} flexWrap="wrap" useFlexGap>
          <Button
            variant="contained"
            startIcon={
              generating && !deepMode ? (
                <CircularProgress size={18} color="inherit" />
              ) : (
                <AutoAwesomeIcon />
              )
            }
            onClick={() => handleGenerate(false)}
            disabled={!canGenerate}
          >
            {suggestions ? 'Re-profile & generate' : 'Profile & generate'}
          </Button>
          <Button
            variant="outlined"
            startIcon={
              generating && deepMode ? (
                <CircularProgress size={18} color="inherit" />
              ) : (
                <BiotechIcon />
              )
            }
            onClick={() => handleGenerate(true)}
            disabled={!canGenerate}
          >
            Extensive profiling & generate
          </Button>
        </Stack>
        {!loadingDetail && versionResources.length === 0 && (
          <Typography variant="caption" color="text.secondary" sx={{mt: 1, display: 'block'}}>
            This version has no BigQuery or GCS resources to profile.
          </Typography>
        )}
        <Typography variant="caption" color="text.secondary" sx={{mt: 1, display: 'block'}}>
          Extensive mode runs deep statistical profiling and an AI investigation
          (querying the data and searching the web) — slower, but richer.
        </Typography>
        {(generating || progress.length > 0) && (
          <ProgressLog steps={progress} running={generating} />
        )}
      </Box>

      {genError && (
        <Alert severity="error" sx={{mb: 2}}>
          {genError}
        </Alert>
      )}

      {suggestions && (
        <>
          <Divider sx={{mb: 2}} />
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
              mb: 2,
            }}
          >
            <Typography variant="h6">Review suggestions</Typography>
            <Button
              variant="contained"
              color="success"
              startIcon={saving ? <CircularProgress size={18} color="inherit" /> : <SaveIcon />}
              onClick={handleSave}
              disabled={saving}
            >
              Save approved values
            </Button>
          </Box>

          <CompletenessSummary suggestions={suggestions} edits={edits} />

          <Stack spacing={3}>
            {suggestions.map((s) => (
              <SuggestionRow
                key={`${runId}:${s.key}`}
                suggestion={s}
                value={edits[s.key] ?? {}}
                onChange={(v) => setEdit(s.key, v)}
              />
            ))}
          </Stack>

          <Box sx={{mt: 3, textAlign: 'right'}}>
            <Button
              variant="contained"
              color="success"
              startIcon={saving ? <CircularProgress size={18} color="inherit" /> : <SaveIcon />}
              onClick={handleSave}
              disabled={saving}
            >
              Save approved values
            </Button>
          </Box>
        </>
      )}

      <Snackbar
        open={!!saveMsg}
        autoHideDuration={4000}
        onClose={() => setSaveMsg(null)}
        message={saveMsg ?? ''}
      />
      <Snackbar open={!!saveErr} autoHideDuration={6000} onClose={() => setSaveErr(null)}>
        <Alert severity="error" onClose={() => setSaveErr(null)}>
          {saveErr}
        </Alert>
      </Snackbar>
    </Box>
  );
}
