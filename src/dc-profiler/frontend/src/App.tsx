import {useEffect, useState} from 'react';
import AppBar from '@mui/material/AppBar';
import Toolbar from '@mui/material/Toolbar';
import Typography from '@mui/material/Typography';
import Container from '@mui/material/Container';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Alert from '@mui/material/Alert';
import CircularProgress from '@mui/material/CircularProgress';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import {Routes, Route, useNavigate, useParams, useLocation} from 'react-router-dom';
import type {DataCollection} from './api/types';
import {listDataCollections} from './api/client';
import {DataCollectionPicker} from './components/DataCollectionPicker';
import {MetadataEditor} from './components/MetadataEditor';

/**
 * Top-level app: pick a data collection, then review/edit/save AI-suggested
 * catalog metadata for it. The selected collection is reflected in the URL
 * (`/dc/<slug>`) so it is deep-linkable and the browser back button works.
 */
export function App() {
  const location = useLocation();
  const navigate = useNavigate();
  const onEditor = location.pathname.startsWith('/dc/');

  return (
    <Box sx={{display: 'flex', flexDirection: 'column', minHeight: '100vh'}}>
      <AppBar position="static" color="default" elevation={1}>
        <Toolbar>
          {onEditor && (
            <Button startIcon={<ArrowBackIcon />} onClick={() => navigate('/')} sx={{mr: 2}}>
              Collections
            </Button>
          )}
          <Typography variant="h6" component="h1" sx={{flexGrow: 1}}>
            Data Collection Profiler
          </Typography>
        </Toolbar>
      </AppBar>

      <Container maxWidth="lg" sx={{py: 4, flexGrow: 1}}>
        <Routes>
          <Route path="/" element={<PickerPage />} />
          <Route path="/dc/:slug" element={<EditorPage />} />
        </Routes>
      </Container>
    </Box>
  );
}

// PickerPage lists writable collections and navigates to the editor on select,
// using the collection's user-facing ID (slug) as the URL segment.
function PickerPage() {
  const navigate = useNavigate();
  return (
    <DataCollectionPicker
      onSelect={(dc) => navigate(`/dc/${encodeURIComponent(dc.userFacingId || dc.id)}`)}
    />
  );
}

// EditorPage resolves the URL slug to a DataCollection (via the list endpoint,
// which returns both the uuid and the slug) so the page is deep-linkable.
function EditorPage() {
  const {slug} = useParams();
  const navigate = useNavigate();
  const [dc, setDc] = useState<DataCollection | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setDc(null);
    setError(null);
    listDataCollections()
      .then((r) => {
        if (!active) return;
        const found = (r.dataCollections ?? []).find(
          (d) => d.userFacingId === slug || d.id === slug
        );
        if (found) setDc(found);
        else setError(`Data collection "${slug}" was not found, or you don't have edit access to it.`);
      })
      .catch((e: Error) => {
        if (active) setError(e.message);
      });
    return () => {
      active = false;
    };
  }, [slug]);

  if (error) {
    return (
      <Box>
        <Alert severity="error" sx={{mb: 2}}>
          {error}
        </Alert>
        <Button startIcon={<ArrowBackIcon />} onClick={() => navigate('/')}>
          Back to collections
        </Button>
      </Box>
    );
  }

  if (!dc) {
    return (
      <Box sx={{display: 'flex', justifyContent: 'center', py: 8}}>
        <CircularProgress />
      </Box>
    );
  }

  return <MetadataEditor dc={dc} onBack={() => navigate('/')} />;
}
