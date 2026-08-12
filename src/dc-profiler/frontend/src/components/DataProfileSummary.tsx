import Accordion from '@mui/material/Accordion';
import AccordionDetails from '@mui/material/AccordionDetails';
import AccordionSummary from '@mui/material/AccordionSummary';
import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import StorageIcon from '@mui/icons-material/Storage';
import TableChartIcon from '@mui/icons-material/TableChart';
import type {BucketProfile, DataProfile, Resource, TableProfile} from '../api/types';

interface Props {
  resources: Resource[];
  profile: DataProfile | null;
}

/**
 * Summarizes the data collection's underlying assets: the WSM resources always,
 * and — once suggestions have been generated — the deeper data profile
 * (headline stats, per-table schema, per-bucket file-type mix) the suggestions
 * were based on.
 */
export function DataProfileSummary({resources, profile}: Props) {
  const buckets = resources.filter((r) => r.resourceType === 'GCS_BUCKET');
  const datasets = resources.filter((r) => r.resourceType === 'BIG_QUERY_DATASET');

  return (
    <Accordion defaultExpanded variant="outlined">
      <AccordionSummary expandIcon={<ExpandMoreIcon />}>
        <Typography fontWeight={600}>
          Data assets ({datasets.length} BigQuery dataset{datasets.length === 1 ? '' : 's'},{' '}
          {buckets.length} bucket{buckets.length === 1 ? '' : 's'})
        </Typography>
      </AccordionSummary>
      <AccordionDetails>
        <Stack spacing={1} sx={{mb: profile ? 2 : 0}}>
          {resources.length === 0 && (
            <Typography color="text.secondary">No cloud resources found.</Typography>
          )}
          {resources.map((r) => (
            <Box key={r.name} sx={{display: 'flex', alignItems: 'center', gap: 1}}>
              {r.resourceType === 'GCS_BUCKET' ? (
                <StorageIcon fontSize="small" color="action" />
              ) : (
                <TableChartIcon fontSize="small" color="action" />
              )}
              <Typography variant="body2">
                <strong>{r.name}</strong>{' '}
                {r.bucketName
                  ? `gs://${r.bucketName}`
                  : r.datasetId
                    ? `${r.projectId}.${r.datasetId}`
                    : r.resourceType}
              </Typography>
            </Box>
          ))}
        </Stack>

        {profile && <ProfileInsights profile={profile} />}
      </AccordionDetails>
    </Accordion>
  );
}

function ProfileInsights({profile}: {profile: DataProfile}) {
  const tables = profile.tables ?? [];
  const buckets = profile.buckets ?? [];

  const totalRows = tables.reduce((n, t) => n + (t.numRows || 0), 0);
  const totalTableBytes = tables.reduce((n, t) => n + (t.sizeBytes || 0), 0);
  const totalObjects = buckets.reduce((n, b) => n + (b.numObjects || 0), 0);
  const totalBucketBytes = buckets.reduce((n, b) => n + (b.totalBytes || 0), 0);

  const totalCols = tables.reduce((n, t) => n + t.columns.length, 0);
  const documentedCols = tables.reduce(
    (n, t) => n + t.columns.filter((c) => (c.description ?? '').trim() !== '').length,
    0
  );
  const docPct = totalCols ? Math.round((documentedCols / totalCols) * 100) : 0;

  return (
    <Box>
      {/* Headline stats */}
      <Box sx={{display: 'flex', flexWrap: 'wrap', gap: 1.5, mb: 2}}>
        {tables.length > 0 && (
          <>
            <Stat label="Tables" value={tables.length.toLocaleString()} />
            <Stat label="Total rows" value={totalRows.toLocaleString()} />
            <Stat label="BigQuery size" value={formatBytes(totalTableBytes)} />
            <Stat label="Columns documented" value={`${docPct}%`} sub={`${documentedCols}/${totalCols}`} />
          </>
        )}
        {buckets.length > 0 && (
          <>
            <Stat label="Buckets" value={buckets.length.toLocaleString()} />
            <Stat label="Objects" value={totalObjects.toLocaleString()} />
            <Stat label="Storage size" value={formatBytes(totalBucketBytes)} />
          </>
        )}
      </Box>

      {tables.length > 0 && (
        <Box sx={{mb: 2}}>
          <Typography variant="subtitle2" gutterBottom>
            Tables profiled
          </Typography>
          <Stack spacing={0.5}>
            {tables.map((t) => (
              <Typography key={t.fqn} variant="body2" color="text.secondary">
                <code>{t.fqn}</code> — {t.numRows.toLocaleString()} rows, {t.columns.length} columns
                {t.sizeBytes ? `, ${formatBytes(t.sizeBytes)}` : ''}
                {t.columns.length ? `, ${tableDocPct(t)}% documented` : ''}
              </Typography>
            ))}
          </Stack>
        </Box>
      )}

      {buckets.length > 0 && (
        <Box sx={{mb: 2}}>
          <Typography variant="subtitle2" gutterBottom>
            Buckets profiled
          </Typography>
          <Stack spacing={1}>
            {buckets.map((b) => (
              <Box key={b.bucket}>
                <Typography variant="body2" color="text.secondary">
                  <code>{b.bucket}</code> — {b.numObjects.toLocaleString()} objects,{' '}
                  {formatBytes(b.totalBytes)}
                  {b.truncated ? ' (sampled)' : ''}
                </Typography>
                <FileTypeChips bucket={b} />
              </Box>
            ))}
          </Stack>
        </Box>
      )}

      {profile.errors && profile.errors.length > 0 && (
        <Box>
          <Typography variant="subtitle2" color="warning.main" gutterBottom>
            Could not read some assets
          </Typography>
          <Stack spacing={0.5}>
            {profile.errors.map((e, i) => (
              <Chip
                key={i}
                label={e}
                size="small"
                color="warning"
                variant="outlined"
                sx={{maxWidth: '100%', height: 'auto', '& .MuiChip-label': {whiteSpace: 'normal', py: 0.5}}}
              />
            ))}
          </Stack>
        </Box>
      )}
    </Box>
  );
}

// Stat is a compact headline metric card.
function Stat({label, value, sub}: {label: string; value: string; sub?: string}) {
  return (
    <Box
      sx={{
        minWidth: 120,
        px: 1.5,
        py: 1,
        border: '1px solid',
        borderColor: 'divider',
        borderRadius: 1,
        bgcolor: 'background.paper',
      }}
    >
      <Typography variant="h6" sx={{lineHeight: 1.2}}>
        {value}
      </Typography>
      <Typography variant="caption" color="text.secondary">
        {label}
        {sub ? ` · ${sub}` : ''}
      </Typography>
    </Box>
  );
}

// FileTypeChips shows the mix of file extensions in a bucket (from the sampled
// object list), most common first.
function FileTypeChips({bucket}: {bucket: BucketProfile}) {
  const objects = bucket.objects ?? [];
  if (objects.length === 0) return null;

  const counts = new Map<string, number>();
  for (const o of objects) {
    const m = /\.([a-z0-9]+)$/i.exec(o.name);
    const ext = m ? m[1].toLowerCase() : '(no ext)';
    counts.set(ext, (counts.get(ext) ?? 0) + 1);
  }
  const sorted = [...counts.entries()].sort((a, b) => b[1] - a[1]).slice(0, 8);

  return (
    <Box sx={{display: 'flex', flexWrap: 'wrap', gap: 0.5, mt: 0.5}}>
      {sorted.map(([ext, n]) => (
        <Chip key={ext} label={`${ext} ×${n}`} size="small" variant="outlined" />
      ))}
    </Box>
  );
}

function tableDocPct(t: TableProfile): number {
  if (t.columns.length === 0) return 0;
  const documented = t.columns.filter((c) => (c.description ?? '').trim() !== '').length;
  return Math.round((documented / t.columns.length) * 100);
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let val = n / 1024;
  let i = 0;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val.toFixed(1)} ${units[i]}`;
}
