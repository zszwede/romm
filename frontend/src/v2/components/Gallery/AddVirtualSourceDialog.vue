<script setup lang="ts">
// AddVirtualSourceDialog: assign one or more archive.org index URLs to a
// platform, and manage the sources already attached to it. The backend lists
// the games at each URL and adds them as file-less "virtual" ROMs
// (is_virtual=true) carrying metadata, downloadable on demand.
//
// Target platform is chosen from the full supported-platforms catalog via the
// shared PlatformSelect (same searchable dropdown as Library Management's
// "RomM platform" column), defaulting to the platform the dialog opened from.
// Picking a catalog platform that isn't in the library yet (e.g. Apple II)
// creates it — so a not-yet-existing system can be seeded from here without
// typing a slug. v2 sibling of ScanPlatformDialog.
//
// The "Current sources" section lists the archive.org sources already on the
// platform the dialog opened from, with inline region-filter editing, an
// enable/disable toggle, and delete (deleting a source leaves the virtual ROMs
// already created from it in place).
import { RBtn, RDialog, RSelect, RSwitch, RTextField } from "@v2/lib";
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import platformApi from "@/services/api/platform";
import virtualSourceApi, {
  type VirtualSource,
} from "@/services/api/virtualSource";
import storePlatforms, { type Platform } from "@/stores/platforms";
import { useSnackbar } from "@/v2/composables/useSnackbar";
import PlatformSelect from "@/v2/components/shared/PlatformSelect.vue";

defineOptions({ inheritAttrs: false });

const props = defineProps<{
  modelValue: boolean;
  platform: Platform;
}>();

const emit = defineEmits<{
  (e: "update:modelValue", v: boolean): void;
  // Fired after at least one source is assigned, so the parent can refresh
  // the grid as the background sync creates virtual ROMs.
  (e: "assigned"): void;
}>();

const { t } = useI18n();
const snackbar = useSnackbar();
const platformsStore = storePlatforms();

const REGION_OPTIONS = ["USA", "Europe", "Japan", "World"];

// The full catalog of RomM-supported platforms (hundreds of entries) — the
// same list the folder-mapping "RomM platform" cell offers.
const supportedPlatforms = ref<Platform[]>([]);
const loadingPlatforms = ref(false);

const targetSlug = ref<string | null>(null);
const urlsText = ref("");
const regionFilter = ref<string[]>([]);
const syncNow = ref(true);
const submitting = ref(false);

// --- existing sources on the platform the dialog opened from ---------------
const existingSources = ref<VirtualSource[]>([]);
const loadingSources = ref(false);
// Local, per-row region selections (so edits stay reactive and isolated).
const rowRegions = reactive<Record<number, string[]>>({});
const confirmingDeleteId = ref<number | null>(null);
const busyId = ref<number | null>(null);

const sortedPlatforms = computed(() =>
  [...supportedPlatforms.value].sort((a, b) =>
    a.display_name.localeCompare(b.display_name),
  ),
);

const urls = computed(() =>
  urlsText.value
    .split("\n")
    .map((u) => u.trim())
    .filter((u) => u.length > 0),
);

const canSubmit = computed(
  () => urls.value.length > 0 && !!targetSlug.value && !submitting.value,
);

async function ensureSupportedPlatforms() {
  if (supportedPlatforms.value.length || loadingPlatforms.value) return;
  loadingPlatforms.value = true;
  try {
    const { data } = await platformApi.getSupportedPlatforms();
    supportedPlatforms.value = data ?? [];
  } catch {
    // Fall back to just the known/library platforms so the picker still works.
    supportedPlatforms.value = platformsStore.allPlatforms as Platform[];
  } finally {
    loadingPlatforms.value = false;
  }
}

async function loadSources() {
  if (props.platform?.id == null) {
    existingSources.value = [];
    return;
  }
  loadingSources.value = true;
  try {
    const { data } = await virtualSourceApi.getSources({
      platformId: props.platform.id,
    });
    existingSources.value = data ?? [];
    for (const s of existingSources.value) {
      rowRegions[s.id] = [...(s.region_filter ?? [])];
    }
  } catch {
    existingSources.value = [];
  } finally {
    loadingSources.value = false;
  }
}

function errorText(err: unknown): string {
  const e = err as {
    response?: { data?: { detail?: string; msg?: string } };
    message?: string;
  };
  return (
    e?.response?.data?.detail ||
    e?.response?.data?.msg ||
    e?.message ||
    t("common.unknown-error")
  );
}

function lastResultText(s: VirtualSource): string {
  if (!s.enabled) return t("platform.virtual-source-disabled");
  if (!s.last_synced_at) return t("platform.virtual-source-never-synced");
  return s.last_result ?? "";
}

async function saveRegions(source: VirtualSource) {
  busyId.value = source.id;
  try {
    const { data } = await virtualSourceApi.updateSource({
      id: source.id,
      regionFilter: rowRegions[source.id] ?? [],
    });
    source.region_filter = data.region_filter;
    snackbar.success(t("platform.virtual-source-updated"), {
      icon: "mdi-check-bold",
    });
  } catch (err) {
    // Roll the row back to what the server still has.
    rowRegions[source.id] = [...(source.region_filter ?? [])];
    snackbar.error(
      t("platform.virtual-source-update-failed", { error: errorText(err) }),
      { icon: "mdi-close-circle" },
    );
  } finally {
    busyId.value = null;
  }
}

function regionsDirty(source: VirtualSource): boolean {
  const a = [...(rowRegions[source.id] ?? [])].sort();
  const b = [...(source.region_filter ?? [])].sort();
  return a.length !== b.length || a.some((v, i) => v !== b[i]);
}

async function toggleEnabled(source: VirtualSource, value: boolean) {
  busyId.value = source.id;
  try {
    const { data } = await virtualSourceApi.updateSource({
      id: source.id,
      enabled: value,
    });
    source.enabled = data.enabled;
  } catch (err) {
    source.enabled = !value; // revert the switch
    snackbar.error(
      t("platform.virtual-source-update-failed", { error: errorText(err) }),
      { icon: "mdi-close-circle" },
    );
  } finally {
    busyId.value = null;
  }
}

async function confirmDelete(source: VirtualSource) {
  busyId.value = source.id;
  try {
    await virtualSourceApi.deleteSource({ id: source.id });
    existingSources.value = existingSources.value.filter(
      (s) => s.id !== source.id,
    );
    delete rowRegions[source.id];
    snackbar.success(t("platform.virtual-source-deleted"), {
      icon: "mdi-check-bold",
    });
  } catch (err) {
    snackbar.error(
      t("platform.virtual-source-delete-failed", { error: errorText(err) }),
      { icon: "mdi-close-circle" },
    );
  } finally {
    confirmingDeleteId.value = null;
    busyId.value = null;
  }
}

// Reset the form and default to the current platform each time it opens.
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      targetSlug.value = props.platform.slug;
      urlsText.value = "";
      regionFilter.value = [];
      syncNow.value = true;
      confirmingDeleteId.value = null;
      void ensureSupportedPlatforms();
      void loadSources();
    }
  },
  { immediate: true },
);

function closeDialog() {
  emit("update:modelValue", false);
}

async function submit() {
  if (!canSubmit.value) return;
  const slug = targetSlug.value;
  if (!slug) return;
  submitting.value = true;
  // If the chosen platform already exists in the library, target it by id;
  // otherwise pass its slug so the backend creates it (folders + record).
  const existing = platformsStore.allPlatforms.find(
    (p) => p.slug === slug || p.fs_slug === slug,
  );
  try {
    const { data } = await virtualSourceApi.assignSources({
      urls: urls.value,
      platformId: existing ? existing.id : undefined,
      fsSlug: existing ? undefined : slug,
      regionFilter: regionFilter.value.length ? regionFilter.value : undefined,
      sync: syncNow.value,
    });
    snackbar.success(
      syncNow.value
        ? t("platform.virtual-sources-added-syncing", { n: data.length })
        : t("platform.virtual-sources-added", { n: data.length }),
      { icon: "mdi-check-bold" },
    );
    emit("update:modelValue", false);
    if (syncNow.value && data.length) emit("assigned");
  } catch (err) {
    snackbar.error(
      t("platform.virtual-sources-failed", { error: errorText(err) }),
      { icon: "mdi-close-circle" },
    );
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <RDialog
    :model-value="modelValue"
    icon="mdi-cloud-download-outline"
    :width="560"
    cancelable
    @update:model-value="$emit('update:modelValue', $event)"
    @close="closeDialog"
  >
    <template #header>
      <span>{{ t("platform.add-virtual-source") }}</span>
    </template>

    <template #content>
      <div class="r-v2-add-vsrc">
        <div class="r-v2-add-vsrc__field">
          <label class="r-v2-add-vsrc__label">{{
            t("platform.assign-to")
          }}</label>
          <PlatformSelect
            v-model="targetSlug"
            :items="sortedPlatforms"
            item-key="slug"
            searchable
            :loading="loadingPlatforms"
            :placeholder="t('common.select')"
            :search-placeholder="t('common.search')"
          />
          <p class="r-v2-add-vsrc__hint">
            {{ t("platform.virtual-target-hint") }}
          </p>
        </div>

        <RTextField
          v-model="urlsText"
          multiline
          :rows="4"
          :label="t('platform.archive-org-urls')"
          :hint="t('platform.archive-org-urls-hint')"
          persistent-hint
        />

        <RSelect
          v-model="regionFilter"
          :items="REGION_OPTIONS"
          :label="t('platform.region-filter')"
          multiple
          chips
          clearable
        />

        <RSwitch v-model="syncNow" :label="t('platform.sync-now')" />

        <!-- Manage sources already attached to the platform we opened from. -->
        <div class="r-v2-add-vsrc__manage">
          <label class="r-v2-add-vsrc__label">{{
            t("platform.virtual-current-sources", { platform: platform.name })
          }}</label>

          <p
            v-if="!loadingSources && existingSources.length === 0"
            class="r-v2-add-vsrc__hint"
          >
            {{ t("platform.virtual-no-sources") }}
          </p>

          <ul v-else class="r-v2-vsrc-list">
            <li
              v-for="s in existingSources"
              :key="s.id"
              class="r-v2-vsrc-row"
              :class="{ 'r-v2-vsrc-row--off': !s.enabled }"
            >
              <div class="r-v2-vsrc-row__top">
                <a
                  class="r-v2-vsrc-row__url"
                  :href="s.url"
                  :title="s.url"
                  target="_blank"
                  rel="noopener noreferrer"
                  >{{ s.url }}</a
                >
                <template v-if="confirmingDeleteId === s.id">
                  <RBtn
                    size="small"
                    variant="translucent"
                    color="danger"
                    :loading="busyId === s.id"
                    @click="confirmDelete(s)"
                  >
                    {{ t("common.delete") }}
                  </RBtn>
                  <RBtn
                    size="small"
                    variant="translucent"
                    :disabled="busyId === s.id"
                    @click="confirmingDeleteId = null"
                  >
                    {{ t("common.cancel") }}
                  </RBtn>
                </template>
                <RBtn
                  v-else
                  size="small"
                  variant="translucent"
                  color="danger"
                  icon="mdi-trash-can-outline"
                  :aria-label="t('common.delete')"
                  :tooltip="t('platform.virtual-source-confirm-delete')"
                  @click="confirmingDeleteId = s.id"
                />
              </div>

              <div class="r-v2-vsrc-row__ctl">
                <RSelect
                  v-model="rowRegions[s.id]"
                  :items="REGION_OPTIONS"
                  :placeholder="t('platform.region-filter')"
                  multiple
                  chips
                  clearable
                  density="compact"
                  class="r-v2-vsrc-row__regions"
                />
                <RBtn
                  v-if="regionsDirty(s)"
                  size="small"
                  variant="translucent"
                  color="primary"
                  icon="mdi-content-save-outline"
                  :aria-label="t('common.save')"
                  :tooltip="t('common.save')"
                  :loading="busyId === s.id"
                  @click="saveRegions(s)"
                />
                <RSwitch
                  :model-value="s.enabled"
                  :disabled="busyId === s.id"
                  @update:model-value="toggleEnabled(s, $event)"
                />
              </div>

              <p class="r-v2-add-vsrc__hint">{{ lastResultText(s) }}</p>
            </li>
          </ul>
        </div>
      </div>
    </template>

    <template #footer>
      <RBtn
        variant="translucent"
        color="primary"
        prepend-icon="mdi-cloud-download-outline"
        :disabled="!canSubmit"
        :loading="submitting"
        @click="submit"
      >
        {{ t("common.add") }}
      </RBtn>
    </template>
  </RDialog>
</template>

<style scoped>
.r-v2-add-vsrc {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.r-v2-add-vsrc__field {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.r-v2-add-vsrc__label {
  font-size: 11px;
  font-weight: var(--r-font-weight-semibold);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--r-color-fg-muted);
}
.r-v2-add-vsrc__hint {
  margin: 0;
  font-size: 11px;
  color: var(--r-color-fg-muted);
}
.r-v2-add-vsrc__manage {
  display: flex;
  flex-direction: column;
  gap: 8px;
  border-top: 1px solid var(--r-color-border, rgba(255, 255, 255, 0.1));
  padding-top: 12px;
}
.r-v2-vsrc-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-height: 260px;
  overflow-y: auto;
}
.r-v2-vsrc-row {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 8px;
  border-radius: 8px;
  background: var(--r-color-bg-subtle, rgba(255, 255, 255, 0.04));
}
.r-v2-vsrc-row--off {
  opacity: 0.55;
}
.r-v2-vsrc-row__top {
  display: flex;
  align-items: center;
  gap: 8px;
}
.r-v2-vsrc-row__url {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--r-color-fg, inherit);
  text-decoration: none;
}
.r-v2-vsrc-row__url:hover {
  text-decoration: underline;
}
.r-v2-vsrc-row__ctl {
  display: flex;
  align-items: center;
  gap: 8px;
}
.r-v2-vsrc-row__regions {
  flex: 1 1 auto;
  min-width: 0;
}
</style>
