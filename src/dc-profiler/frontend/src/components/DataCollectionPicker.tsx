import {useEffect, useState} from 'react';
import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import CardActionArea from '@mui/material/CardActionArea';
import CardContent from '@mui/material/CardContent';
import Chip from '@mui/material/Chip';
import CircularProgress from '@mui/material/CircularProgress';
import Alert from '@mui/material/Alert';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import type {DataCollection} from '../api/types';
import {listDataCollections} from '../api/client';

interface Props {
  onSelect: (dc: DataCollection) => void;
}

/**
 * Lists every data collection the signed-in user can write to (OWNER/WRITER)
 * and lets them pick one to work on.
 */
export function DataCollectionPicker({onSelect}: Props) {
  const [dcs, setDcs] = useState<DataCollection[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState('');

  useEffect(() => {
    let active = true;
    listDataCollections()
      .then((r) => {
        if (active) setDcs(r.dataCollections ?? []);
      })
      .catch((e: Error) => {
        if (active) setError(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  if (loading) {
    return (
      <Box sx={{display: 'flex', justifyContent: 'center', py: 8}}>
        <CircularProgress />
      </Box>
    );
  }

  if (error) {
    return <Alert severity="error">Failed to load data collections: {error}</Alert>;
  }

  const q = filter.trim().toLowerCase();
  const visible = q
    ? dcs.filter(
        (dc) =>
          dc.displayName?.toLowerCase().includes(q) ||
          dc.userFacingId?.toLowerCase().includes(q)
      )
    : dcs;

  return (
    <Box>
      <Typography variant="h5" gutterBottom>
        Select a data collection
      </Typography>
      <Typography color="text.secondary" sx={{mb: 3}}>
        Showing {dcs.length} collection{dcs.length === 1 ? '' : 's'} you can edit
        (OWNER or WRITER).
      </Typography>

      <TextField
        fullWidth
        size="small"
        label="Filter by name or ID"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        sx={{mb: 3}}
      />

      {visible.length === 0 ? (
        <Alert severity="info">No matching data collections.</Alert>
      ) : (
        <Stack spacing={2}>
          {visible.map((dc) => (
            <Card key={dc.id} variant="outlined">
              <CardActionArea onClick={() => onSelect(dc)}>
                <CardContent>
                  <Box
                    sx={{
                      display: 'flex',
                      justifyContent: 'space-between',
                      alignItems: 'center',
                      gap: 2,
                    }}
                  >
                    <Typography variant="h6">{dc.displayName || dc.userFacingId}</Typography>
                    <Chip label={dc.highestRole} size="small" color="primary" variant="outlined" />
                  </Box>
                  <Typography variant="body2" color="text.secondary">
                    {dc.userFacingId}
                  </Typography>
                  {dc.description && (
                    <Typography
                      variant="body2"
                      sx={{
                        mt: 1,
                        display: '-webkit-box',
                        WebkitLineClamp: 2,
                        WebkitBoxOrient: 'vertical',
                        overflow: 'hidden',
                      }}
                    >
                      {dc.description}
                    </Typography>
                  )}
                </CardContent>
              </CardActionArea>
            </Card>
          ))}
        </Stack>
      )}
    </Box>
  );
}
