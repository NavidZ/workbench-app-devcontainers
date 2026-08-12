import {useEffect, useRef} from 'react';
import Box from '@mui/material/Box';
import CircularProgress from '@mui/material/CircularProgress';
import Typography from '@mui/material/Typography';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import type {Progress} from '../api/types';
import {useWittyLine} from '../hooks/useWittyLine';

interface Props {
  steps: Progress[];
  running: boolean;
}

/**
 * ProgressLog shows the live, streamed steps of the generation pipeline: each
 * profiled table/bucket and the Gemini call, so the user watches real progress
 * instead of an opaque "this can take a minute" message. It auto-scrolls to the
 * newest line while running.
 */
export function ProgressLog({steps, running}: Props) {
  const endRef = useRef<HTMLDivElement>(null);
  const witty = useWittyLine(running);

  useEffect(() => {
    endRef.current?.scrollIntoView({block: 'nearest'});
  }, [steps.length]);

  return (
    <Box
      sx={{
        mt: 1.5,
        p: 1.5,
        maxHeight: 220,
        overflow: 'auto',
        border: '1px solid',
        borderColor: 'divider',
        borderRadius: 1,
        bgcolor: 'grey.50',
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
        fontSize: 13,
      }}
    >
      {steps.map((s, i) => {
        const isLast = i === steps.length - 1;
        const spinning = running && isLast;
        return (
          <Box key={i} sx={{display: 'flex', alignItems: 'center', gap: 1, py: 0.25}}>
            {spinning ? (
              <CircularProgress size={13} thickness={6} />
            ) : (
              <CheckCircleIcon sx={{fontSize: 15, color: 'success.main'}} />
            )}
            <Typography
              component="span"
              sx={{
                fontFamily: 'inherit',
                fontSize: 'inherit',
                color: spinning ? 'text.primary' : 'text.secondary',
                fontWeight: spinning ? 600 : 400,
                whiteSpace: 'pre',
              }}
            >
              {s.message}
              {s.total ? ` (${s.current}/${s.total})` : ''}
            </Typography>
          </Box>
        );
      })}
      {running && steps.length === 0 && (
        <Box sx={{display: 'flex', alignItems: 'center', gap: 1}}>
          <CircularProgress size={13} thickness={6} />
          <Typography component="span" sx={{fontFamily: 'inherit', fontSize: 'inherit'}}>
            Starting…
          </Typography>
        </Box>
      )}
      {witty && (
        <Typography
          component="div"
          sx={{
            mt: 0.5,
            pl: 3,
            fontFamily: 'inherit',
            fontSize: 12,
            fontStyle: 'italic',
            color: 'text.disabled',
          }}
        >
          {witty}
        </Typography>
      )}
      <div ref={endRef} />
    </Box>
  );
}
