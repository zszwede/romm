import api from "@/services/api";

export const virtualSourceApi = api;

export interface VirtualSource {
  id: number;
  platform_id: number;
  url: string;
  kind: string | null;
  region_filter: string[] | null;
  enabled: boolean;
  last_synced_at: string | null;
  last_result: string | null;
}

async function assignSources({
  urls,
  platformId,
  fsSlug,
  regionFilter,
  sync = true,
}: {
  urls: string[];
  platformId?: number | undefined;
  fsSlug?: string | undefined;
  regionFilter?: string[] | undefined;
  sync?: boolean;
}) {
  return api.post<VirtualSource[]>("/virtual/assign", {
    urls,
    platform_id: platformId ?? null,
    fs_slug: fsSlug ?? null,
    region_filter: regionFilter ?? null,
    sync,
  });
}

async function getSources({ platformId }: { platformId?: number } = {}) {
  return api.get<VirtualSource[]>("/virtual/sources", {
    params: platformId != null ? { platform_id: platformId } : {},
  });
}

async function updateSource({
  id,
  url,
  regionFilter,
  enabled,
}: {
  id: number;
  url?: string | undefined;
  regionFilter?: string[] | null | undefined;
  enabled?: boolean | undefined;
}) {
  const body: Record<string, unknown> = {};
  if (url !== undefined) body.url = url;
  if (regionFilter !== undefined) body.region_filter = regionFilter;
  if (enabled !== undefined) body.enabled = enabled;
  return api.patch<VirtualSource>(`/virtual/sources/${id}`, body);
}

async function deleteSource({ id }: { id: number }) {
  return api.delete<{ msg: string }>(`/virtual/sources/${id}`);
}

async function syncPlatform({ platformId }: { platformId: number }) {
  return api.post<{ msg: string }>(`/virtual/platforms/${platformId}/sync`);
}

// Join two or more entries of the same game into one. The backend keeps the
// local (physical) copy when present, else the identified one, and folds every
// other's archive.org URLs into it as download mirrors.
async function merge({ romIds }: { romIds: number[] }) {
  return api.post<{
    msg: string;
    keeper_id: number;
    absorbed_ids: number[];
  }>("/virtual/merge", { rom_ids: romIds });
}

// Remove one download source (mirror) from a ROM.
async function removeSource({ romId, url }: { romId: number; url: string }) {
  return api.delete<{ msg: string }>(`/virtual/roms/${romId}/source`, {
    data: { url },
  });
}

export default {
  assignSources,
  getSources,
  updateSource,
  deleteSource,
  syncPlatform,
  merge,
  removeSource,
};
