import Box from '@mui/material/Box';
import CircularProgress from '@mui/material/CircularProgress';
import Typography from '@mui/material/Typography';
import {useWittyLine} from '../hooks/useWittyLine';

interface Props {
  message: string;
}

/**
 * LoadingNote is a centered spinner with a concrete status message telling the
 * user what is being fetched, plus a rotating witty line. Used for page-level
 * waits (e.g. the initial data-collection load).
 */
export function LoadingNote({message}: Props) {
  const witty = useWittyLine(true);
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: 1.5,
        py: 6,
      }}
    >
      <CircularProgress />
      <Typography color="text.secondary">{message}</Typography>
      {witty && (
        <Typography variant="caption" sx={{fontStyle: 'italic', color: 'text.disabled'}}>
          {witty}
        </Typography>
      )}
    </Box>
  );
}
