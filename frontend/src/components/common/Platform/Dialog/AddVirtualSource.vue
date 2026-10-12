<script setup lang="ts">
// AddVirtualSource: assign one or more archive.org index URLs to a platform.
// The backend lists the games at each URL and adds them as file-less "virtual"
// ROMs (is_virtual=true) that carry metadata and can be downloaded on demand.
// A platform can be an existing one or a new one created from a slug.
import type { Emitter } from "mitt";
import { computed, inject, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import RDialog from "@/components/common/RDialog.vue";
import virtualSourceApi from "@/services/api/virtualSource";
import storeRoms from "@/stores/roms";
import type { Platform } from "@/stores/platforms";
import type { Events } from "@/types/emitter";

const { t } = useI18n();
const { mdAndUp } = useDisplay();
const emitter = inject<Emitter<Events>>("emitter");
const romsStore = storeRoms();

const REGION_OPTIONS = ["USA", "Europe", "Japan", "World"];

const show = ref(false);
const submitting = ref(false);
const platform = ref<Platform | null>(null);
const newSlug = ref("");
const urlsText = ref("");
const regionFilter = ref<string[]>([]);
const syncNow = ref(true);

const urls = computed(() =>
  urlsText.value
    .split("\n")
    .map((u) => u.trim())
    .filter((u) => u.length > 0),
);

const canSubmit = computed(
  () => urls.value.length > 0 && (platform.value != null || newSlug.value.trim()),
);

emitter?.on("showAddVirtualSourceDialog", (p) => {
  platform.value = p;
  newSlug.value = "";
  urlsText.value = "";
  regionFilter.value = [];
  syncNow.value = true;
  show.value = true;
});

function closeDialog() {
  show.value = false;
}

async function submit() {
  if (submitting.value || !canSubmit.value) return;
  submitting.value = true;
  const slug = newSlug.value.trim();
  try {
    const { data } = await virtualSourceApi.assignSources({
      urls: urls.value,
      // A typed slug always creates/targets by slug; otherwise use the platform.
      platformId: slug ? undefined : (platform.value?.id ?? undefined),
      fsSlug: slug || undefined,
      regionFilter: regionFilter.value.length ? regionFilter.value : undefined,
      sync: syncNow.value,
    });
    emitter?.emit("snackbarShow", {
      msg: syncNow.value
        ? `Added ${data.length} source(s); syncing virtual games in the background`
        : `Added ${data.length} source(s)`,
      icon: "mdi-check-bold",
      color: "green",
      timeout: 4000,
    });
    show.value = false;
    // Refresh in place when viewing the same platform (new virtual games appear
    // as the background sync creates them; a later manual refresh shows more).
    if (
      syncNow.value &&
      data.length &&
      romsStore.currentPlatform?.id === data[0]?.platform_id
    ) {
      emitter?.emit("filterRoms", null);
    }
  } catch (err) {
    const e = err as {
      response?: { data?: { msg?: string; detail?: string } };
      message?: string;
    };
    emitter?.emit("snackbarShow", {
      msg: `Couldn't assign sources: ${
        e?.response?.data?.detail ||
        e?.response?.data?.msg ||
        e?.message ||
        "unknown error"
      }`,
      icon: "mdi-close-circle",
      color: "red",
      timeout: 5000,
    });
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <RDialog
    v-model="show"
    icon="mdi-cloud-download-outline"
    :width="mdAndUp ? '45vw' : '95vw'"
    @close="closeDialog"
  >
    <template #header>
      <v-card-title>{{ t("platform.add-virtual-source") }}</v-card-title>
    </template>
    <template #content>
      <v-row class="pa-2" no-gutters>
        <v-col>
          <div v-if="platform" class="text-body-2 mb-2">
            {{ t("platform.assign-to") }}:
            <strong>{{ platform.name }}</strong>
          </div>
          <v-text-field
            v-model="newSlug"
            :label="t('platform.new-platform-slug')"
            :hint="t('platform.new-platform-slug-hint')"
            persistent-hint
            variant="outlined"
            density="comfortable"
          />
        </v-col>
      </v-row>
      <v-row class="pa-2" no-gutters>
        <v-col>
          <v-textarea
            v-model="urlsText"
            :label="t('platform.archive-org-urls')"
            :hint="t('platform.archive-org-urls-hint')"
            persistent-hint
            variant="outlined"
            rows="4"
            auto-grow
          />
        </v-col>
      </v-row>
      <v-row class="pa-2" no-gutters>
        <v-col>
          <v-select
            v-model="regionFilter"
            :items="REGION_OPTIONS"
            :label="t('platform.region-filter')"
            variant="outlined"
            density="comfortable"
            multiple
            chips
            clearable
            hide-details
          />
        </v-col>
      </v-row>
      <v-row class="pa-2 align-center" no-gutters>
        <v-checkbox
          :model-value="syncNow"
          :label="t('platform.sync-now')"
          hide-details
          density="compact"
          @update:model-value="syncNow = $event ?? false"
        />
      </v-row>
    </template>
    <template #append>
      <v-row class="justify-center pa-2" no-gutters>
        <v-btn-group divided density="compact">
          <v-btn class="bg-toplayer text-romm-red" @click="closeDialog">
            {{ t("common.cancel") }}
          </v-btn>
          <v-btn
            class="bg-toplayer text-romm-green"
            :disabled="!canSubmit || submitting"
            :loading="submitting"
            @click="submit"
          >
            {{ t("common.add") }}
          </v-btn>
        </v-btn-group>
      </v-row>
    </template>
  </RDialog>
</template>
