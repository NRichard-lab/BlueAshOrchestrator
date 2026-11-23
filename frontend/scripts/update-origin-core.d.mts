export const UPSTREAM_HOST_SUFFIXES: string[];
export function isUpstreamHost(hostname: string): boolean;
export type UpdateOriginResult = { ok: true; origin: string } | { ok: false; reason: string };
export function parseUpdateOrigin(raw: unknown): UpdateOriginResult;
