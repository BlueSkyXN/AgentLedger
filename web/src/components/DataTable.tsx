import { useMemo, useState } from "react";
import type { ReactNode } from "react";

type SortDirection = "asc" | "desc";
type SortValue = string | number | null | undefined;

export type DataTableColumn<T> = {
  key: string;
  label: string;
  render: (row: T) => ReactNode;
  value?: (row: T) => SortValue;
  className?: string;
  numeric?: boolean;
  sortable?: boolean;
};

type DataTableProps<T> = {
  rows: T[];
  columns: Array<DataTableColumn<T>>;
  rowKey: (row: T) => string;
  emptyText: string;
  defaultSortKey?: string;
  defaultDirection?: SortDirection;
  initialLimit?: number;
};

const limitOptions = [20, 50, 100, 200, 0];

function compareValues(a: SortValue, b: SortValue, direction: SortDirection): number {
  if (a == null && b == null) return 0;
  if (a == null) return 1;
  if (b == null) return -1;
  const modifier = direction === "asc" ? 1 : -1;
  if (typeof a === "number" && typeof b === "number") {
    return (a - b) * modifier;
  }
  return String(a).localeCompare(String(b), "zh-CN", { numeric: true }) * modifier;
}

export function DataTable<T>({
  rows,
  columns,
  rowKey,
  emptyText,
  defaultSortKey,
  defaultDirection = "desc",
  initialLimit = 50,
}: DataTableProps<T>) {
  const [sortKey, setSortKey] = useState(defaultSortKey ?? columns.find((column) => column.value)?.key ?? "");
  const [direction, setDirection] = useState<SortDirection>(defaultDirection);
  const [limit, setLimit] = useState(initialLimit);
  const sortColumn = columns.find((column) => column.key === sortKey && column.value && column.sortable !== false);

  const visibleRows = useMemo(() => {
    const sorted = sortColumn
      ? [...rows].sort((a, b) => compareValues(sortColumn.value?.(a), sortColumn.value?.(b), direction))
      : [...rows];
    return limit > 0 ? sorted.slice(0, limit) : sorted;
  }, [direction, limit, rows, sortColumn]);

  function toggleSort(column: DataTableColumn<T>) {
    if (!column.value || column.sortable === false) return;
    if (column.key === sortKey) {
      setDirection((current) => current === "asc" ? "desc" : "asc");
      return;
    }
    setSortKey(column.key);
    setDirection(column.numeric ? "desc" : "asc");
  }

  return (
    <div className="data-table">
      <div className="table-toolbar">
        <span>{visibleRows.length} / {rows.length} 行</span>
        <label className="select-label">
          显示
          <select value={limit} onChange={(event) => setLimit(Number(event.target.value))}>
            {limitOptions.map((value) => <option key={value} value={value}>{value === 0 ? "全部" : value}</option>)}
          </select>
        </label>
      </div>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              {columns.map((column) => {
                const sortable = Boolean(column.value && column.sortable !== false);
                const active = sortKey === column.key && sortable;
                return (
                  <th key={column.key} className={column.numeric ? "numeric" : undefined}>
                    {sortable ? (
                      <button type="button" className={`sort-button ${active ? "active" : ""}`} onClick={() => toggleSort(column)}>
                        {column.label}
                        <span>{active ? (direction === "asc" ? "↑" : "↓") : "↕"}</span>
                      </button>
                    ) : column.label}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {visibleRows.map((row) => (
              <tr key={rowKey(row)}>
                {columns.map((column) => (
                  <td key={column.key} className={[column.className, column.numeric ? "numeric" : ""].filter(Boolean).join(" ") || undefined}>
                    {column.render(row)}
                  </td>
                ))}
              </tr>
            ))}
            {rows.length === 0 ? (
              <tr>
                <td colSpan={columns.length} className="empty-cell">{emptyText}</td>
              </tr>
            ) : null}
          </tbody>
        </table>
      </div>
    </div>
  );
}
