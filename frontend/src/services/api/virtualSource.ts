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

async function deleteSource({ id }: { id: number }) {
  return api.delete<{ msg: string }>(`/virtual/sources/${id}`);
}

async function syncPlatform({ platformId }: { platformId: number }) {
  return api.post<{ msg: string }>(`/virtual/platforms/${platformId}/sync`);
}

export default {
  assignSources,
  getSources,
  deleteSource,
  syncPlatform,
};
