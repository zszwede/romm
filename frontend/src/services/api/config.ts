import type { AxiosResponse } from "axios";
import type {
  ConvertoSettingsPayload,
  ExclusionPayload,
  PlatformBindingPayload,
  ScanSettingsPayload,
} from "@/__generated__";
import api from "@/services/api";

export const configApi = api;

async function addPlatformBindConfig({
  fsSlug,
  slug,
}: {
  fsSlug: string;
  slug: string;
}) {
  return api.post<void, AxiosResponse<void>, PlatformBindingPayload>(
    "/config/system/platforms",
    { fs_slug: fsSlug, slug },
  );
}

async function deletePlatformBindConfig({ fsSlug }: { fsSlug: string }) {
  return api.delete(`/config/system/platforms/${encodeURIComponent(fsSlug)}`);
}

async function addPlatformVersionConfig({
  fsSlug,
  slug,
}: {
  fsSlug: string;
  slug: string;
}) {
  return api.post<void, AxiosResponse<void>, PlatformBindingPayload>(
    "/config/system/versions",
    { fs_slug: fsSlug, slug },
  );
}

async function deletePlatformVersionConfig({ fsSlug }: { fsSlug: string }) {
  return api.delete(`/config/system/versions/${encodeURIComponent(fsSlug)}`);
}

async function addExclusion({
  exclusionValue,
  exclusionType,
}: {
  exclusionValue: string;
  exclusionType: string;
}) {
  return api.post<void, AxiosResponse<void>, ExclusionPayload>(
    "/config/exclude",
    {
      exclusion_value: exclusionValue,
      exclusion_type: exclusionType,
    },
  );
}

async function deleteExclusion({
  exclusionValue,
  exclusionType,
}: {
  exclusionValue: string;
  exclusionType: string;
}) {
  return api.delete(
    `/config/exclude/${exclusionType}/${encodeURIComponent(exclusionValue)}`,
  );
}

async function updateScanSettings(payload: ScanSettingsPayload) {
  return api.put<void, AxiosResponse<void>, ScanSettingsPayload>(
    "/config/scan",
    payload,
  );
}

async function updateConvertoSettings(payload: ConvertoSettingsPayload) {
  return api.put<void, AxiosResponse<void>, ConvertoSettingsPayload>(
    "/config/converto_settings",
    payload,
  );
}

export interface DownloadsSettingsPayload {
  merge_service_url: string;
  service_api_token: string;
  callback_token: string;
  host_tokens: Record<string, string>;
  default_host_order: string[];
}

/** Admin-only: the virtual-ROM download workflow settings (carries secrets). */
async function getDownloadsSettings() {
  return api.get<DownloadsSettingsPayload>("/config/downloads_settings");
}

async function updateDownloadsSettings(payload: DownloadsSettingsPayload) {
  return api.put<void, AxiosResponse<void>, DownloadsSettingsPayload>(
    "/config/downloads_settings",
    payload,
  );
}

export default {
  addPlatformBindConfig,
  deletePlatformBindConfig,
  addPlatformVersionConfig,
  deletePlatformVersionConfig,
  addExclusion,
  deleteExclusion,
  updateScanSettings,
  updateConvertoSettings,
  getDownloadsSettings,
  updateDownloadsSettings,
};
