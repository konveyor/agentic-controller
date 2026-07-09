/** Small display helpers shared by the pages. */

export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

/** kubectl-style age: 42s, 12m, 3h, 5d. */
export function formatAge(creationTimestamp?: string): string {
  if (!creationTimestamp) return "—";
  const ms = Date.now() - new Date(creationTimestamp).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "—";
  const s = Math.floor(ms / 1000);
  if (s < 120) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 120) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h`;
  return `${Math.floor(h / 24)}d`;
}

/** AgentRun.status.duration is wall-clock seconds. */
export function formatDuration(seconds?: number): string {
  if (seconds === undefined || !Number.isFinite(seconds)) return "—";
  const s = Math.round(seconds);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rest = s % 60;
  return rest ? `${m}m ${rest}s` : `${m}m`;
}

export function truncate(text: string, max: number): string {
  const t = text.trim();
  return t.length <= max ? t : `${t.slice(0, max - 1)}…`;
}
