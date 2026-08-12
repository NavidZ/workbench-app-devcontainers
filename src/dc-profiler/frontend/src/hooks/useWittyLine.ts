import {useEffect, useState} from 'react';
import {WITTY_LINES} from '../data/wittyLines';

// useWittyLine rotates a cosmetic "still working" line every 10s while active,
// starting from a random spot so repeat runs don't show the same opener.
export function useWittyLine(active: boolean): string | null {
  const [i, setI] = useState(() => Math.floor(Math.random() * WITTY_LINES.length));
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setI((n) => (n + 1) % WITTY_LINES.length), 10000);
    return () => clearInterval(t);
  }, [active]);
  return active ? WITTY_LINES[i] : null;
}
