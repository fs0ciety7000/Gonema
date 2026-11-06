import { useQueryClient } from '@tanstack/react-query';
import { CircleCheck, ExternalLink, KeyRound, Lock } from 'lucide-react';
import { useEffect, useState } from 'react';
import { ApiError } from '@/api/client';
import { keys } from '@/api/queries';
import { screenKeys, setTmdbKey } from '@/api/screens';
import { Button, buttonClass } from '@/components/ui/Button';
import { Dialog } from '@/components/ui/Dialog';
import { Field, Input } from '@/components/ui/Field';
import { toast } from '@/components/ui/Toast';

/** Minimal TMDB key form (the full Settings screen comes in lot 3): PUT /settings/tmdb checks the key with TMDB. */
export function TmdbKeyDialog({
  open,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved?: () => void;
}) {
  const qc = useQueryClient();
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    if (open) {
      setValue('');
      setError(null);
    }
  }, [open]);

  const save = async () => {
    const v = value.trim();
    if (v.length < 16) {
      setError('That is too short for a TMDB key. Paste the API Read Access Token (the long one) or the API Key.');
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await setTmdbKey(v);
      void qc.invalidateQueries({ queryKey: screenKeys.tmdb });
      void qc.invalidateQueries({ queryKey: keys.status });
      void qc.invalidateQueries({ queryKey: ['lookup'] });
      void qc.invalidateQueries({ queryKey: ['discover'] });
      onOpenChange(false);
      toast.show({
        title: 'TMDB key saved',
        body: 'It works: posters, dates and collections now come from TMDB.',
        icon: <CircleCheck className="i" />,
      });
      onSaved?.();
    } catch (e) {
      setError(
        e instanceof ApiError
          ? e.isNetwork
            ? 'Gonema could not be reached. Nothing was saved; try again in a moment.'
            : `${e.detail} Check that you copied the whole key.`
          : 'The key could not be checked. Try again.',
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      icon={<KeyRound className="i" aria-hidden="true" />}
      title="Connect film metadata"
      description="Gonema uses The Movie Database for search, posters, cast, collections and release dates. The key is yours, free, and stays encrypted on this server."
      actions={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button variant="primary" loading={saving} onClick={() => void save()}>
            Check and save
          </Button>
        </>
      }
    >
      <form
        className="tmdb-form"
        onSubmit={(ev) => {
          ev.preventDefault();
          void save();
        }}
      >
        <ol className="tmdb-steps">
          <li>Create a free account on themoviedb.org.</li>
          <li>Open Settings › API and request a “Developer” key; any short description is fine.</li>
          <li>Copy the API Read Access Token (the long one) and paste it below.</li>
        </ol>
        <a
          className={buttonClass('secondary', 'sm', 'tmdb-link')}
          href="https://www.themoviedb.org/settings/api"
          target="_blank"
          rel="noreferrer noopener"
        >
          Open TMDB API settings
          <ExternalLink className="i" aria-hidden="true" />
          <span className="sr-only">(opens in a new tab)</span>
        </a>
        <Field label="API Read Access Token" error={error ?? undefined}>
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              mono
              icon={<Lock className="i" aria-hidden="true" />}
              value={value}
              invalid={invalid}
              aria-describedby={describedBy}
              autoComplete="off"
              spellCheck={false}
              placeholder="eyJhbGciOiJIUzI1NiJ9…"
              onChange={(ev) => setValue(ev.target.value)}
            />
          )}
        </Field>
      </form>
    </Dialog>
  );
}
