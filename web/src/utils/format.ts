export function formatInt(value: number | null | undefined): string {
  return value == null ? "-" : new Intl.NumberFormat("zh-CN").format(value);
}

export function formatCost(value: number | null | undefined): string {
  return value == null ? "-" : `$${value.toFixed(4)}`;
}

export function formatEstimatedCost(
  value: number | null | undefined,
  pricing: { status: string; priced_events: number; unpriced_events: number; policy_zero_events: number } | null | undefined,
): string {
  if (value == null || pricing?.status !== "available") return "不可用";
  if (pricing.priced_events === 0 && pricing.unpriced_events > 0) return "不可用";
  return formatCost(value);
}

export function formatPercent(value: number | null | undefined): string {
  return value == null ? "-" : `${(value * 100).toFixed(1)}%`;
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
