<script setup lang="ts">
// AddVirtualSourceDialog: assign one or more archive.org index URLs to a
// platform. The backend lists the games at each URL and adds them as
// file-less "virtual" ROMs (is_virtual=true) carrying metadata, downloadable
// on demand.
//
// Target platform is chosen from the full supported-platforms catalog via the
// shared PlatformSelect (same searchable dropdown as Library Management's
// "RomM platform" column), defaulting to the platform the dialog opened from.
// Picking a catalog platform that isn't in the library yet (e.g. Apple II)
// creates it — so a not-yet-existing system can be seeded from here without
// typing a slug. v2 sibling of ScanPlatformDialog.
import { RBtn, RDialog, RSelect, RSwitch, RTextField } from "@v2/lib";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import platformApi from "@/services/api/platform";
import virtualSourceApi from "@/services/api/virtualSource";
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

// Reset the form and default to the current platform each time it opens.
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      targetSlug.value = props.platform.slug;
      urlsText.value = "";
      regionFilter.value = [];
      syncNow.value = true;
      void ensureSupportedPlatforms();
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
    const e = err as {
      response?: { data?: { detail?: string; msg?: string } };
      message?: string;
    };
    snackbar.error(
      t("platform.virtual-sources-failed", {
        error:
          e?.response?.data?.detail ||
          e?.response?.data?.msg ||
          e?.message ||
          t("common.unknown-error"),
      }),
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
</style>
