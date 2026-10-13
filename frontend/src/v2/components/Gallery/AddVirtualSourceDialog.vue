<script setup lang="ts">
// AddVirtualSourceDialog: assign one or more archive.org index URLs to a
// platform. The backend lists the games at each URL and adds them as
// file-less "virtual" ROMs (is_virtual=true) carrying metadata, downloadable
// on demand. Targets the current platform by default; a typed slug instead
// creates/targets a platform by slug (so a not-yet-existing system like
// "apple2" can be seeded from here). v2 sibling of the ScanPlatformDialog.
import { RBtn, RDialog, RSelect, RSwitch, RTextField } from "@v2/lib";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import virtualSourceApi from "@/services/api/virtualSource";
import type { Platform } from "@/stores/platforms";
import { useSnackbar } from "@/v2/composables/useSnackbar";
import PlatformIcon from "@/v2/components/shared/PlatformIcon.vue";

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

const REGION_OPTIONS = ["USA", "Europe", "Japan", "World"];

const newSlug = ref("");
const urlsText = ref("");
const regionFilter = ref<string[]>([]);
const syncNow = ref(true);
const submitting = ref(false);

const urls = computed(() =>
  urlsText.value
    .split("\n")
    .map((u) => u.trim())
    .filter((u) => u.length > 0),
);

const canSubmit = computed(() => urls.value.length > 0 && !submitting.value);

// Reset the form each time the dialog opens so a previous run's URLs don't
// linger.
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      newSlug.value = "";
      urlsText.value = "";
      regionFilter.value = [];
      syncNow.value = true;
    }
  },
);

function closeDialog() {
  emit("update:modelValue", false);
}

async function submit() {
  if (!canSubmit.value) return;
  submitting.value = true;
  const slug = newSlug.value.trim();
  try {
    const { data } = await virtualSourceApi.assignSources({
      urls: urls.value,
      // A typed slug always creates/targets by slug; otherwise this platform.
      platformId: slug ? undefined : props.platform.id,
      fsSlug: slug || undefined,
      regionFilter: regionFilter.value.length ? regionFilter.value : undefined,
      sync: syncNow.value,
    });
    snackbar.success(
      syncNow.value
        ? t("platform.virtual-sources-added-syncing", {
            n: data.length,
          })
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
        <!-- Platform identity row: sibling of the ScanPlatformDialog head. -->
        <div class="r-v2-add-vsrc__head">
          <div class="r-v2-add-vsrc__icon">
            <PlatformIcon
              :slug="platform.slug"
              :fs-slug="platform.fs_slug"
              :alt="platform.display_name"
              :size="40"
            />
          </div>
          <div class="r-v2-add-vsrc__meta">
            <p class="r-v2-add-vsrc__label">{{ t("platform.assign-to") }}</p>
            <p class="r-v2-add-vsrc__name" :title="platform.display_name">
              {{ platform.display_name }}
            </p>
          </div>
        </div>

        <RTextField
          v-model="newSlug"
          :label="t('platform.new-platform-slug')"
          :hint="t('platform.new-platform-slug-hint')"
          persistent-hint
        />

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

.r-v2-add-vsrc__head {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  background: var(--r-color-bg-elevated);
  border: 1px solid var(--r-color-border);
  border-radius: var(--r-radius-md);
}
.r-v2-add-vsrc__icon {
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  display: grid;
  place-items: center;
}
.r-v2-add-vsrc__meta {
  min-width: 0;
  flex: 1;
}
.r-v2-add-vsrc__label {
  margin: 0;
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--r-color-fg-muted);
}
.r-v2-add-vsrc__name {
  margin: 2px 0 0;
  font-size: 13px;
  font-weight: var(--r-font-weight-semibold);
  color: var(--r-color-fg);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
