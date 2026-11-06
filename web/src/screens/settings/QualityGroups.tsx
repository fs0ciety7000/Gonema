import { Ellipsis, Layers, Pencil, Split, SquareStack } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import { QualityBadge } from '@/components/ui/Badges';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox } from '@/components/ui/Checkbox';
import { Dialog } from '@/components/ui/Dialog';
import { Field, Input } from '@/components/ui/Field';
import { Menu, MenuItem, MenuSeparator } from '@/components/ui/Menu';
import {
  defaultGroupName,
  entryLabel,
  groupNameError,
  groupQualities,
  isGroup,
  mergeWithNext,
  type ProfileDraft,
  renameGroup,
  splitGroup,
} from './qualityModel';

/* Quality groups in the profile editor (settings-quality.html: "Group" button, "equal, best score
   wins" rows). Every change edits the draft; Save sends it. Keyboard: the dialog is a form, the
   group menu is a Base UI menu (arrows, Enter), the member's "Take out" is a button. */

type Change = (d: ProfileDraft, said: string) => void;

/** "Group" (create) or "Rename…" (edit) dialog. */
export function GroupDialog({
  open,
  onOpenChange,
  draft,
  names,
  rename,
  onChange,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  draft: ProfileDraft;
  names: ReadonlyMap<string, string>;
  /** Name of the group to rename; absent to create one. */
  rename?: string;
  onChange: Change;
}) {
  const singles = useMemo(
    () =>
      draft.entries
        .filter((e) => !isGroup(e))
        .map((e) => e.qualities?.[0] ?? '')
        .filter(Boolean),
    [draft.entries],
  );
  const [picked, setPicked] = useState<ReadonlySet<string>>(new Set());
  const [name, setName] = useState('');
  const [edited, setEdited] = useState(false);
  const [tried, setTried] = useState(false);
  useEffect(() => {
    if (!open) return;
    setPicked(new Set());
    setName(rename ?? '');
    setEdited(!!rename);
    setTried(false);
  }, [open, rename]);
  const ordered = singles.filter((q) => picked.has(q));
  const shownName = edited ? name : ordered.length >= 2 ? defaultGroupName(ordered, names, draft.entries) : '';
  const nameError = groupNameError(draft.entries, shownName, names, rename);
  const pickError = !rename && ordered.length < 2 ? 'Pick at least two qualities.' : null;
  const submit = () => {
    setTried(true);
    if (nameError || pickError) return;
    if (rename) {
      onChange(renameGroup(draft, rename, shownName), `Group ${rename} renamed ${shownName.trim()}.`);
    } else {
      onChange(
        groupQualities(draft, ordered, shownName),
        `${ordered.length} qualities grouped as ${shownName.trim()}: they count as equal.`,
      );
    }
    onOpenChange(false);
  };
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      icon={<Layers className="i" aria-hidden="true" />}
      title={rename ? `Rename ${rename}` : 'Group qualities'}
      description={
        rename
          ? undefined
          : 'Qualities in a group count as equal: among them, the release with the best score wins. The group takes the place of the best one.'
      }
      className="group-dialog"
      actions={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button variant="primary" type="submit" form="group-form">
            {rename ? 'Rename' : ordered.length >= 2 ? `Group ${ordered.length} qualities` : 'Group'}
          </Button>
        </>
      }
    >
      <form
        id="group-form"
        className="group-form"
        onSubmit={(ev) => {
          ev.preventDefault();
          submit();
        }}
      >
        {!rename && (
          <fieldset>
            <legend className="field-label">Qualities</legend>
            {singles.length < 2 ? (
              <p className="t-secondary">Every quality is already in a group. Split one first.</p>
            ) : (
              <ul className="group-picks">
                {singles.map((q) => (
                  <li key={q}>
                    <Checkbox
                      checked={picked.has(q)}
                      onCheckedChange={(c) =>
                        setPicked((p) => {
                          const n = new Set(p);
                          if (c) n.add(q);
                          else n.delete(q);
                          return n;
                        })
                      }
                      label={
                        <>
                          <span aria-hidden="true">
                            <QualityBadge quality={q} />
                          </span>
                          <span className="sr-only">{names.get(q) ?? q}</span>
                        </>
                      }
                    />
                  </li>
                ))}
              </ul>
            )}
            {tried && pickError && (
              <p className="field-error" role="alert">
                {pickError}
              </p>
            )}
          </fieldset>
        )}
        <Field label="Group name" error={tried || (edited && shownName) ? (nameError ?? undefined) : undefined}>
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              value={shownName}
              maxLength={100}
              invalid={invalid}
              aria-describedby={describedBy}
              placeholder={rename ? undefined : 'Named after the qualities you pick'}
              onChange={(ev) => {
                setEdited(true);
                setName(ev.target.value);
              }}
            />
          )}
        </Field>
      </form>
    </Dialog>
  );
}

/** Menu of a group row: rename, merge with a neighbour, split. */
export function GroupMenu({
  draft,
  index,
  names,
  onRename,
  onChange,
}: {
  draft: ProfileDraft;
  index: number;
  names: ReadonlyMap<string, string>;
  onRename: () => void;
  onChange: Change;
}) {
  const e = draft.entries[index];
  if (!e?.group) return null;
  const prev = draft.entries[index - 1];
  const next = draft.entries[index + 1];
  const merge = (at: number, other: NonNullable<typeof e>) => {
    const d = mergeWithNext(draft, at, names);
    onChange(d, `${entryLabel(other, names)} merged into ${d.entries[at]?.group ?? e.group}.`);
  };
  return (
    <Menu
      label={`Actions for group ${e.group}`}
      align="end"
      trigger={
        <IconButton size="sm" className="gh-more" label={`Actions for group ${e.group}`}>
          <Ellipsis className="i i-sm" aria-hidden="true" />
        </IconButton>
      }
    >
      <MenuItem icon={<Pencil className="i" aria-hidden="true" />} onSelect={onRename}>
        Rename…
      </MenuItem>
      {prev && (
        <MenuItem icon={<SquareStack className="i" aria-hidden="true" />} onSelect={() => merge(index - 1, prev)}>
          Merge with {entryLabel(prev, names)} (above)
        </MenuItem>
      )}
      {next && (
        <MenuItem icon={<SquareStack className="i" aria-hidden="true" />} onSelect={() => merge(index, next)}>
          Merge with {entryLabel(next, names)} (below)
        </MenuItem>
      )}
      <MenuSeparator />
      <MenuItem
        icon={<Split className="i" aria-hidden="true" />}
        onSelect={() =>
          onChange(splitGroup(draft, e.group!), `${e.group} split into ${e.qualities?.length ?? 0} qualities.`)
        }
      >
        Split into single qualities
      </MenuItem>
    </Menu>
  );
}
