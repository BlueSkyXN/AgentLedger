export function formatInt(value: number | null | undefined): string {
  return value == null ? "-" : new Intl.NumberFormat("zh-CN").format(value);
}

export function formatCost(value: number | null | undefined): string {
  return value == null ? "-" : `$${value.toFixed(4)}`;
}

export function formatMs(value: number | null | undefined): string {
  if (value == null) return "-";
  if (value >= 1000) return `${(value / 1000).toFixed(2)}s`;
  return `${Math.round(value)}ms`;
}

export function formatTPS(value: number | null | undefined): string {
  return value == null ? "-" : `${value.toFixed(2)}/s`;
}

export function formatDate(value: string | null | undefined): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function shortHash(value: string | null | undefined): string {
  if (!value) return "-";
  return value.length <= 16 ? value : `${value.slice(0, 10)}...${value.slice(-4)}`;
}
