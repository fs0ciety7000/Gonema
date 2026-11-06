import { CircleCheck, CircleX, Lock } from 'lucide-react';
import { type FormEvent, type ReactNode, useId, useState } from 'react';
import { ApiError } from '@/api/client';
import type { TestResult } from '@/api/schema';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { Dialog } from '@/components/ui/Dialog';
import { Field, Input } from '@/components/ui/Field';
import { Select } from '@/components/ui/Select';
import { Switch } from '@/components/ui/Switch';

/* Spec-driven forms for the connection settings (indexers, clients, media servers, notifications).
   Secret fields are write-only: the API says whether one is stored (…Set: true), never its value.
   Left blank, a stored secret is kept; typing replaces it. */

export type Values = Record<string, unknown>;

export type FieldSpec =
  | {
      kind: 'text' | 'url' | 'mono';
      name: string;
      label: string;
      hint?: string;
      required?: boolean;
      placeholder?: string;
    }
  | { kind: 'number'; name: string; label: string; hint?: string; min?: number; max?: number; step?: number }
  | { kind: 'secret'; name: string; label: string; hint?: string; stored?: boolean }
  | { kind: 'switch'; name: string; label: string; hint?: string }
  | {
      kind: 'select';
      name: string;
      label: string;
      hint?: string;
      options: ReadonlyArray<{ value: string; label: string }>;
    }
  | { kind: 'list'; name: string; label: string; hint?: string; numbers?: boolean }
  | {
      kind: 'multi';
      name: string;
      label: string;
      hint?: string;
      options: ReadonlyArray<{ value: string; label: string }>;
    };

export const str = (v: unknown) => (typeof v === 'string' ? v : v == null ? '' : String(v));
export const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);

/** Comma/space separated input → list (numbers when asked); invalid numbers are dropped. */
export function parseList(text: string, numbers = false): Array<string | number> {
  const parts = text
    .split(/[\s,]+/)
    .map((p) => p.trim())
    .filter(Boolean);
  if (!numbers) return parts;
  return parts.map(Number).filter((n) => Number.isInteger(n) && n >= 0);
}

/** First required field left empty, as an error message per field name. */
export function validate(spec: readonly FieldSpec[], v: Values): Record<string, string> {
  const errs: Record<string, string> = {};
  for (const f of spec) {
    if ((f.kind === 'text' || f.kind === 'url' || f.kind === 'mono') && f.required && !str(v[f.name]).trim())
      errs[f.name] = `${f.label} is required.`;
    if (f.kind === 'url' && str(v[f.name]).trim() && !/^https?:\/\/[^\s]+$/i.test(str(v[f.name]).trim()))
      errs[f.name] = 'Use a full address starting with http:// or https://.';
    if (f.kind === 'number' && v[f.name] !== undefined && v[f.name] !== '') {
      const n = Number(v[f.name]);
      if (!Number.isFinite(n)) errs[f.name] = `${f.label} must be a number.`;
      else if (f.min !== undefined && n < f.min) errs[f.name] = `${f.label} must be ${f.min} or more.`;
      else if (f.max !== undefined && n > f.max) errs[f.name] = `${f.label} must be ${f.max} or less.`;
    }
  }
  return errs;
}

function FieldInput({
  f,
  v,
  set,
  error,
}: {
  f: FieldSpec;
  v: Values;
  set: (name: string, value: unknown) => void;
  error?: string;
}) {
  const labelId = useId();
  if (f.kind === 'switch')
    return (
      <div className="field field-switch">
        <Switch checked={!!v[f.name]} onCheckedChange={(c) => set(f.name, c)}>
          {f.label}
        </Switch>
        {f.hint && <span className="field-hint">{f.hint}</span>}
      </div>
    );
  if (f.kind === 'select')
    return (
      <div className="field">
        <span className="field-label" id={labelId}>
          {f.label}
        </span>
        <Select
          value={str(v[f.name]) || null}
          options={f.options}
          onValueChange={(x) => set(f.name, x)}
          labelledBy={labelId}
        />
        {f.hint && <span className="field-hint">{f.hint}</span>}
      </div>
    );
  if (f.kind === 'multi') {
    const cur = new Set((v[f.name] as string[] | undefined) ?? []);
    return (
      <fieldset className="field field-multi">
        <legend className="field-label">{f.label}</legend>
        <div className="chips">
          {f.options.map((o) => (
            <Chip
              key={o.value}
              pressed={cur.has(o.value)}
              onClick={() => {
                const n = new Set(cur);
                if (n.has(o.value)) n.delete(o.value);
                else n.add(o.value);
                set(
                  f.name,
                  f.options.map((x) => x.value).filter((x) => n.has(x)),
                );
              }}
            >
              {o.label}
            </Chip>
          ))}
        </div>
        {f.hint && <span className="field-hint">{f.hint}</span>}
      </fieldset>
    );
  }
  const secret = f.kind === 'secret';
  return (
    <Field label={f.label} hint={f.hint} error={error}>
      {({ id, describedBy, invalid }) => (
        <Input
          id={id}
          aria-describedby={describedBy}
          invalid={invalid}
          mono={f.kind === 'mono' || f.kind === 'url' || secret || f.kind === 'list'}
          type={secret ? 'password' : f.kind === 'number' ? 'number' : f.kind === 'url' ? 'url' : 'text'}
          inputMode={f.kind === 'number' ? 'decimal' : undefined}
          autoComplete={secret ? 'new-password' : 'off'}
          spellCheck={false}
          icon={secret ? <Lock className="i" aria-hidden="true" /> : undefined}
          placeholder={
            secret
              ? f.stored
                ? 'Saved · leave blank to keep it'
                : 'Not set'
              : f.kind === 'text' || f.kind === 'url' || f.kind === 'mono'
                ? f.placeholder
                : undefined
          }
          min={f.kind === 'number' ? f.min : undefined}
          max={f.kind === 'number' ? f.max : undefined}
          step={f.kind === 'number' ? (f.step ?? 1) : undefined}
          value={
            f.kind === 'list'
              ? Array.isArray(v[f.name])
                ? (v[f.name] as unknown[]).join(', ')
                : str(v[f.name])
              : str(v[f.name])
          }
          onChange={(ev) => set(f.name, ev.target.value)}
        />
      )}
    </Field>
  );
}

export function FormFields({
  spec,
  values,
  onChange,
  errors = {},
}: {
  spec: readonly FieldSpec[];
  values: Values;
  onChange: (v: Values) => void;
  errors?: Record<string, string>;
}) {
  const set = (name: string, value: unknown) => onChange({ ...values, [name]: value });
  return (
    <div className="form-grid">
      {spec.map((f) => (
        <FieldInput key={f.name} f={f} v={values} set={set} error={errors[f.name]} />
      ))}
    </div>
  );
}

/** Normalises the edited values into the request body: numbers parsed, lists split, blank secrets dropped. */
export function toBody(spec: readonly FieldSpec[], v: Values): Values {
  const out: Values = {};
  for (const f of spec) {
    const x = v[f.name];
    if (f.kind === 'secret') {
      if (str(x)) out[f.name] = str(x);
    } else if (f.kind === 'number') {
      if (x !== '' && x !== undefined && x !== null && Number.isFinite(Number(x))) out[f.name] = Number(x);
    } else if (f.kind === 'list') {
      out[f.name] = Array.isArray(x) ? x : parseList(str(x), f.numbers);
    } else if (f.kind === 'switch') out[f.name] = !!x;
    else if (f.kind === 'multi') out[f.name] = Array.isArray(x) ? x : [];
    else if (x !== undefined) out[f.name] = typeof x === 'string' ? x.trim() : x;
  }
  return out;
}

export function errorText(e: unknown) {
  return e instanceof ApiError ? (e.isNetwork ? 'Gonema could not be reached.' : e.detail) : String(e);
}

export function TestLine({ result }: { result: TestResult | null }) {
  if (!result) return null;
  return result.ok ? (
    <span className="ok-line" role="status">
      <CircleCheck className="i" aria-hidden="true" />
      {result.message || 'Connected.'}
    </span>
  ) : (
    <div className="notice" data-tone="failed" role="alert">
      <CircleX className="i" aria-hidden="true" />
      <div className="grow">
        <div className="ntitle">The test failed.</div>
        <div className="nbody">{result.message}</div>
      </div>
    </div>
  );
}

/** Add / edit dialog of a connection: test before saving, secrets write-only. */
export function ConnectionDialog({
  open,
  onOpenChange,
  title,
  description,
  spec,
  initial,
  onTest,
  onSave,
  head,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  title: string;
  description?: ReactNode;
  spec: (v: Values) => readonly FieldSpec[];
  initial: Values;
  onTest?: (body: Values) => Promise<TestResult>;
  onSave: (body: Values) => Promise<unknown>;
  head?: (v: Values, set: (v: Values) => void) => ReactNode;
}) {
  const [values, setValues] = useState<Values>(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [test, setTest] = useState<TestResult | null>(null);
  const [busy, setBusy] = useState<'test' | 'save' | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);
  const fields = spec(values);
  const check = () => {
    const e = validate(fields, values);
    setErrors(e);
    return !Object.keys(e).length;
  };
  const runTest = async () => {
    if (!onTest || !check()) return;
    setBusy('test');
    setTest(null);
    try {
      setTest(await onTest(toBody(fields, values)));
    } catch (e) {
      setTest({ ok: false, message: errorText(e) });
    } finally {
      setBusy(null);
    }
  };
  const submit = async (ev: FormEvent) => {
    ev.preventDefault();
    if (!check()) return;
    setBusy('save');
    setSaveError(null);
    try {
      await onSave(toBody(fields, values));
      onOpenChange(false);
    } catch (e) {
      setSaveError(errorText(e));
    } finally {
      setBusy(null);
    }
  };
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={description}
      wide
      className="conn-dialog"
    >
      <form onSubmit={submit} noValidate className="conn-form">
        {head?.(values, setValues)}
        <FormFields
          spec={fields}
          values={values}
          onChange={(v) => {
            setValues(v);
            setTest(null);
          }}
          errors={errors}
        />
        <div aria-live="polite">
          <TestLine result={test} />
        </div>
        {saveError && (
          <div className="notice" data-tone="failed" role="alert">
            <CircleX className="i" aria-hidden="true" />
            <div className="grow">
              <div className="ntitle">Not saved.</div>
              <div className="nbody">{saveError}</div>
            </div>
          </div>
        )}
        <div className="actions">
          {onTest && (
            <Button variant="ghost" loading={busy === 'test'} onClick={() => void runTest()} className="mr-auto">
              Test
            </Button>
          )}
          <Button onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button type="submit" variant="primary" loading={busy === 'save'}>
            Save
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
