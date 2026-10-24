<script setup lang="ts">
// Editor for the downloads.* config (the virtual-ROM download workflow): the
// external merge service URL, the callback shared secret, per-host account
// tokens, and the server default host order — plus the current user's own
// preferred host order (stored in ui_settings, read back by the backend when
// dispatching a download).
import { RBtn, RIcon, RSpinner, RTextField } from "@v2/lib";
import { computed, onMounted, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useUISettings } from "@/composables/useUISettings";
import configApi, {
  type DownloadsSettingsPayload,
} from "@/services/api/config";
import storeAuth from "@/stores/auth";
import SettingsSaveBar from "@/v2/components/Settings/SettingsSaveBar.vue";
import SettingsSection from "@/v2/components/Settings/SettingsSection.vue";
import { useSnackbar } from "@/v2/composables/useSnackbar";
import { errorMessage } from "@/v2/utils/errorMessage";

const { t } = useI18n();
const snackbar = useSnackbar();
const authStore = storeAuth();
const { downloadHostOrder } = useUISettings();

const canEdit = computed(() =>
  authStore.scopes.includes("platforms.write"),
);

interface HostToken {
  host: string;
  token: string;
}
interface Form {
  mergeServiceUrl: string;
  serviceApiToken: string;
  callbackToken: string;
  hostTokens: HostToken[];
  defaultHostOrder: string;
}

const form = reactive<Form>({
  mergeServiceUrl: "",
  serviceApiToken: "",
  callbackToken: "",
  hostTokens: [],
  defaultHostOrder: "",
});
const savedSnapshot = ref("");
const loading = ref(true);
const saving = ref(false);

function toSnapshot(f: Form): string {
  return JSON.stringify(f);
}
const dirty = computed(() => toSnapshot(form) !== savedSnapshot.value);

function applyPayload(p: DownloadsSettingsPayload) {
  form.mergeServiceUrl = p.merge_service_url ?? "";
  form.serviceApiToken = p.service_api_token ?? "";
  form.callbackToken = p.callback_token ?? "";
  form.hostTokens = Object.entries(p.host_tokens ?? {}).map(([host, token]) => ({
    host,
    token,
  }));
  form.defaultHostOrder = (p.default_host_order ?? []).join(", ");
  savedSnapshot.value = toSnapshot(form);
}

function parseList(value: string): string[] {
  return value
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
}

function formToPayload(): DownloadsSettingsPayload {
  const hostTokens: Record<string, string> = {};
  for (const { host, token } of form.hostTokens) {
    const h = host.trim().toLowerCase();
    if (h) hostTokens[h] = token;
  }
  return {
    merge_service_url: form.mergeServiceUrl.trim(),
    service_api_token: form.serviceApiToken,
    callback_token: form.callbackToken,
    host_tokens: hostTokens,
    default_host_order: parseList(form.defaultHostOrder),
  };
}

onMounted(async () => {
  try {
    const { data } = await configApi.getDownloadsSettings();
    applyPayload(data);
  } catch (err) {
    snackbar.error(errorMessage(err, t("common.unknown-error")));
  } finally {
    loading.value = false;
  }
});

function addHostToken() {
  form.hostTokens.push({ host: "", token: "" });
}
function removeHostToken(i: number) {
  form.hostTokens.splice(i, 1);
}

async function onSave() {
  saving.value = true;
  try {
    await configApi.updateDownloadsSettings(formToPayload());
    savedSnapshot.value = toSnapshot(form);
    snackbar.success(t("settings.downloads-saved"));
  } catch (err) {
    snackbar.error(errorMessage(err, t("common.unknown-error")));
  } finally {
    saving.value = false;
  }
}

function onReset() {
  savedSnapshot.value = "";
  loading.value = true;
  configApi
    .getDownloadsSettings()
    .then(({ data }) => applyPayload(data))
    .finally(() => (loading.value = false));
}

// Per-user preferred host order (ui_settings; auto-synced by useUISettings).
const myHostOrder = computed({
  get: () => (downloadHostOrder.value as string[]).join(", "),
  set: (value: string) => {
    downloadHostOrder.value = parseList(value);
  },
});
</script>

<template>
  <div v-if="loading" class="r-v2-downloads__loading">
    <RSpinner />
  </div>
  <div v-else class="r-v2-section-stack r-v2-downloads">
    <SettingsSection
      :title="t('settings.downloads-service-title')"
      icon="mdi-cloud-upload-outline"
    >
      <div class="r-v2-downloads__field">
        <RTextField
          v-model="form.mergeServiceUrl"
          :label="t('settings.downloads-merge-service-url')"
          placeholder="http://merge-host:8080"
          :disabled="!canEdit"
          hide-details
        />
        <p class="r-v2-downloads__note">
          <RIcon icon="mdi-information-outline" size="13" />
          {{ t("settings.downloads-merge-service-url-desc") }}
        </p>
      </div>
      <div class="r-v2-downloads__field">
        <RTextField
          v-model="form.serviceApiToken"
          type="password"
          :label="t('settings.downloads-service-api-token')"
          :disabled="!canEdit"
          hide-details
        />
        <p class="r-v2-downloads__note">
          <RIcon icon="mdi-information-outline" size="13" />
          {{ t("settings.downloads-service-api-token-desc") }}
        </p>
      </div>
      <div class="r-v2-downloads__field">
        <RTextField
          v-model="form.callbackToken"
          type="password"
          :label="t('settings.downloads-callback-token')"
          :disabled="!canEdit"
          hide-details
        />
        <p class="r-v2-downloads__note">
          <RIcon icon="mdi-information-outline" size="13" />
          {{ t("settings.downloads-callback-token-desc") }}
        </p>
      </div>
    </SettingsSection>

    <SettingsSection
      :title="t('settings.downloads-hosts-title')"
      icon="mdi-server-network"
    >
      <div class="r-v2-downloads__field">
        <div
          v-for="(row, i) in form.hostTokens"
          :key="i"
          class="r-v2-downloads__host-row"
        >
          <RTextField
            v-model="row.host"
            :label="t('settings.downloads-host')"
            placeholder="1fichier.com"
            :disabled="!canEdit"
            hide-details
          />
          <RTextField
            v-model="row.token"
            type="password"
            :label="t('settings.downloads-host-token')"
            :disabled="!canEdit"
            hide-details
          />
          <RBtn
            variant="text"
            icon="mdi-delete-outline"
            :disabled="!canEdit"
            :aria-label="t('common.delete')"
            @click="removeHostToken(i)"
          />
        </div>
        <div>
          <RBtn
            variant="outlined"
            prepend-icon="mdi-plus"
            :disabled="!canEdit"
            @click="addHostToken"
          >
            {{ t("settings.downloads-add-host") }}
          </RBtn>
        </div>
      </div>
      <div class="r-v2-downloads__field">
        <RTextField
          v-model="form.defaultHostOrder"
          :label="t('settings.downloads-default-host-order')"
          placeholder="1fichier.com, archive.org"
          :disabled="!canEdit"
          hide-details
        />
        <p class="r-v2-downloads__note">
          <RIcon icon="mdi-information-outline" size="13" />
          {{ t("settings.downloads-default-host-order-desc") }}
        </p>
      </div>
    </SettingsSection>

    <SettingsSection
      :title="t('settings.downloads-my-prefs-title')"
      icon="mdi-account-cog-outline"
    >
      <div class="r-v2-downloads__field">
        <RTextField
          v-model="myHostOrder"
          :label="t('settings.downloads-my-host-order')"
          placeholder="1fichier.com, archive.org"
          hide-details
        />
        <p class="r-v2-downloads__note">
          <RIcon icon="mdi-information-outline" size="13" />
          {{ t("settings.downloads-my-host-order-desc") }}
        </p>
      </div>
    </SettingsSection>

    <SettingsSaveBar
      :visible="dirty && canEdit"
      :label="t('settings.downloads-unsaved-changes')"
      :saving="saving"
      @save="onSave"
      @discard="onReset"
    />
  </div>
</template>

<style scoped>
.r-v2-downloads__loading {
  display: grid;
  place-items: center;
  min-height: 240px;
}
.r-v2-downloads {
  padding-bottom: 72px;
}
.r-v2-downloads__field {
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.r-v2-downloads__note {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--r-color-fg-faint);
}
.r-v2-downloads__host-row {
  display: grid;
  grid-template-columns: minmax(140px, 220px) 1fr auto;
  align-items: center;
  gap: 12px;
}
html[data-bp~="xs"] .r-v2-downloads__host-row {
  grid-template-columns: 1fr;
}
</style>
