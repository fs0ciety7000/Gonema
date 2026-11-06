import { ArrowUpDown, Layers, LayoutGrid, ListChecks, Rows3 } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { FilterChip } from '@/components/ui/Chip';
import {
  Menu,
  MenuCheckboxItem,
  MenuGroupLabel,
  MenuRadioGroup,
  MenuRadioItem,
  MenuSeparator,
} from '@/components/ui/Menu';
import { Segmented } from '@/components/ui/Segmented';
import { STATUS } from '@/lib/status';
import { COLUMNS, type ColumnId } from './LibraryTable';
import {
  type Density,
  type Filters,
  GROUP_LABEL,
  GROUPS,
  type GroupKey,
  type Layout,
  type Resolution,
  SORT_LABEL,
  type SortDir,
  type SortKey,
} from './model';

const RES_LABEL: Record<Resolution, string> = { '2160p': '4K', '1080p': '1080p', '720p': '720p', sd: 'SD' };
const ADDED: ReadonlyArray<{ value: number; label: string }> = [
  { value: 7, label: 'Last 7 days' },
  { value: 30, label: 'Last 30 days' },
  { value: 90, label: 'Last 90 days' },
  { value: 365, label: 'Last year' },
];

export function SortMenu({
  sort,
  dir,
  onChange,
}: {
  sort: SortKey;
  dir: SortDir;
  onChange: (s: SortKey, d?: SortDir) => void;
}) {
  return (
    <Menu
      label="Sort"
      align="end"
      trigger={
        <Button
          variant="ghost"
          size="sm"
          icon={<ArrowUpDown className="i" aria-hidden="true" />}
          aria-label={`Sort by ${SORT_LABEL[sort]}, ${dir === 'asc' ? 'ascending' : 'descending'}`}
        >
          <span className="lbl">Sort</span> <span className="val">{SORT_LABEL[sort]}</span>
        </Button>
      }
    >
      <MenuRadioGroup value={sort} onValueChange={(s) => onChange(s as SortKey)}>
        {(Object.keys(SORT_LABEL) as SortKey[]).map((k) => (
          <MenuRadioItem key={k} value={k} selected={k === sort}>
            {SORT_LABEL[k]}
          </MenuRadioItem>
        ))}
      </MenuRadioGroup>
      <MenuSeparator />
      <MenuRadioGroup value={dir} onValueChange={(d) => onChange(sort, d as SortDir)}>
        <MenuRadioItem value="asc" selected={dir === 'asc'}>
          Ascending
        </MenuRadioItem>
        <MenuRadioItem value="desc" selected={dir === 'desc'}>
          Descending
        </MenuRadioItem>
      </MenuRadioGroup>
    </Menu>
  );
}

export function GroupMenu({ group, onChange }: { group: GroupKey; onChange: (g: GroupKey) => void }) {
  return (
    <Menu
      label="Group"
      align="end"
      trigger={
        <Button
          variant="ghost"
          size="sm"
          icon={<Layers className="i" aria-hidden="true" />}
          aria-label={`Group by ${GROUP_LABEL[group]}`}
        >
          <span className="lbl">Group</span> <span className="val">{GROUP_LABEL[group]}</span>
        </Button>
      }
    >
      <MenuRadioGroup value={group} onValueChange={(g) => onChange(g as GroupKey)}>
        {GROUPS.map((g) => (
          <MenuRadioItem key={g} value={g} selected={g === group}>
            {GROUP_LABEL[g]}
          </MenuRadioItem>
        ))}
      </MenuRadioGroup>
    </Menu>
  );
}

export function ColumnsMenu({
  columns,
  onChange,
}: {
  columns: ReadonlySet<ColumnId>;
  onChange: (c: Set<ColumnId>) => void;
}) {
  return (
    <Menu
      label="Columns"
      align="end"
      trigger={
        <Button variant="ghost" size="sm" icon={<ListChecks className="i" aria-hidden="true" />}>
          Columns
        </Button>
      }
    >
      <MenuGroupLabel>Show columns</MenuGroupLabel>
      {COLUMNS.map((c) => (
        <MenuCheckboxItem
          key={c.id}
          checked={columns.has(c.id)}
          onCheckedChange={(on) => {
            const next = new Set(columns);
            if (on) next.add(c.id);
            else next.delete(c.id);
            onChange(next);
          }}
        >
          {c.label}
        </MenuCheckboxItem>
      ))}
    </Menu>
  );
}

export function LayoutControls({
  layout,
  density,
  onLayout,
  onDensity,
}: {
  layout: Layout;
  density: Density;
  onLayout: (l: Layout) => void;
  onDensity: (d: Density) => void;
}) {
  return (
    <>
      <Segmented
        label="Layout"
        iconOnly
        value={layout}
        options={[
          {
            value: 'grid',
            ariaLabel: 'Posters',
            title: 'Posters  G',
            label: <LayoutGrid className="i" aria-hidden="true" />,
          },
          { value: 'table', ariaLabel: 'Table', title: 'Table  T', label: <Rows3 className="i" aria-hidden="true" /> },
        ]}
        onChange={onLayout}
      />
      <Segmented
        label="Density"
        value={density}
        options={[
          { value: 'airy', label: 'Airy' },
          { value: 'compact', label: 'Compact' },
        ]}
        onChange={onDensity}
      />
    </>
  );
}

/** "Showing" chips: each active filter, removable, plus Clear all. */
export function ActiveFilters({
  filters,
  onChange,
  onClear,
}: {
  filters: Filters;
  onChange: (f: Partial<Filters>) => void;
  onClear: () => void;
}) {
  const chips: Array<{ k: string; v: string; clear: Partial<Filters> }> = [];
  if (filters.monitored !== undefined)
    chips.push({ k: 'Monitored', v: filters.monitored ? 'Yes' : 'No', clear: { monitored: undefined } });
  if (filters.status?.length)
    chips.push({ k: 'Status', v: filters.status.map((s) => STATUS[s].label).join(', '), clear: { status: undefined } });
  if (filters.resolution)
    chips.push({ k: 'Resolution', v: RES_LABEL[filters.resolution], clear: { resolution: undefined } });
  if (filters.yearFrom || filters.yearTo)
    chips.push({
      k: 'Year',
      v:
        filters.yearFrom && filters.yearTo
          ? `${filters.yearFrom}–${filters.yearTo}`
          : filters.yearFrom
            ? `from ${filters.yearFrom}`
            : `until ${filters.yearTo}`,
      clear: { yearFrom: undefined, yearTo: undefined },
    });
  if (filters.genre) chips.push({ k: 'Genre', v: filters.genre, clear: { genre: undefined } });
  if (filters.added)
    chips.push({
      k: 'Added',
      v: ADDED.find((a) => a.value === filters.added)?.label ?? `${filters.added} days`,
      clear: { added: undefined },
    });
  if (filters.q) chips.push({ k: 'Search', v: `“${filters.q}”`, clear: { q: undefined } });
  if (!chips.length) return null;
  return (
    // biome-ignore lint/a11y/useSemanticElements: a labelled group of filter chips
    <div className="active-filters" role="group" aria-label="Active filters">
      <span className="lbl">Showing</span>
      {chips.map((c) => (
        <FilterChip key={c.k} k={c.k} value={c.v} onRemove={() => onChange(c.clear)} />
      ))}
      <Button variant="ghost" onClick={onClear}>
        Clear all
      </Button>
    </div>
  );
}
