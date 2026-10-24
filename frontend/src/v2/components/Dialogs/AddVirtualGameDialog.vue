<script setup lang="ts">
// AddVirtualGameDialog: manually add a downloadable (virtual) game to a
// platform from one or more host URLs (1fichier / archive.org, etc.). The
// backend stores it as a file-less virtual Rom routed through that platform's
// download pipeline; a game already on the platform gets the URL added as a
// mirror instead of a duplicate. Switch supports base + update + DLC; other
// platforms normally just use a single "base" link.
import { RBtn, RForm, RSelect, RTextField } from "@v2/lib";
import { RDialog } from "@v2/lib";
import { storeToRefs } from "pinia";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import romApi from "@/services/api/rom";
import type { ManualVirtualLink } from "@/services/api/rom";
import type { Platform } from "@/stores/platforms";
import storePlatforms from "@/stores/platforms";
import PlatformSelect from "@/v2/components/shared/PlatformSelect.vue";
import { useBreakpoint } from "@/v2/composables/useBreakpoint";
import { useEmitterEvent } from "@/v2/composables/useEmitterEvent";
import { useSnackbar } from "@/v2/composables/useSnackbar";
import storeGalleryRoms from "@/v2/stores/galleryRoms";

defineOptions({ inheritAttrs: false });

const { t } = useI18n();
const { mdAndUp } = useBreakpoint();
const snackbar = useSnackbar();

const platformsStore = storePlatforms();
const { allPlatforms } = storeToRefs(platformsStore);
const galleryRoms = storeGalleryRoms();

type Row = { url: string; category: "base" | "update" | "dlc" };

const categoryItems = [
  { value: "base", title: "Base game" },
  { value: "update", title: "Update" },
  { value: "dlc", title: "DLC" },
];

const show = ref(false);
const submitting = ref(false);
const platformId = ref<number | null>(null);
const name = ref("");
const links = ref<Row[]>([{ url: "", category: "base" }]);

const filledRows = computed(() =>
  links.value.filter((r) => r.url.trim().length > 0),
);
const canSubmit = computed(
  () =>
    platformId.value != null &&
    name.value.trim().length > 0 &&
    filledRows.value.length > 0,
);

function addRow() {
  links.value.push({ url: "", category: "base" });
}
function removeRow(i: number) {
  links.value.splice(i, 1);
  if (links.value.length === 0) addRow();
}

const openHandler = (platform: Platform | null) => {
  platformId.value = platform?.id ?? null;
  name.value = "";
  links.value = [{ url: "", category: "base" }];
  show.value = true;
};
useEmitterEvent("showAddVirtualGameDialog", openHandler);

function close() {
  show.value = false;
}

async function submit() {
  if (submitting.value || !canSubmit.value || platformId.value == null) return;
  submitting.value = true;
  try {
    const payload: ManualVirtualLink[] = filledRows.value.map((r) => ({
      url: r.url.trim(),
      category: r.category,
    }));
    const { data } = await romApi.addManualRom({
      platformId: platformId.value,
      name: name.value.trim(),
      links: payload,
    });
    snackbar.success(
      data.outcome === "linked"
        ? t("rom.virtual-game-linked")
        : t("rom.virtual-game-added"),
      { icon: "mdi-check-bold" },
    );
    show.value = false;
    if (galleryRoms.currentPlatform?.id === data.platform_id) {
      galleryRoms.invalidateWindows();
      await galleryRoms.fetchInitialMetadata();
    }
  } catch (err) {
    const e = err as {
      response?: { data?: { msg?: string; detail?: string } };
      message?: string;
    };
    snackbar.error(
      `${t("rom.virtual-game-add-failed")}: ${
        e?.response?.data?.msg ||
        e?.response?.data?.detail ||
        e?.message ||
        t("common.unknown-error")
      }`,
      { icon: "mdi-close-circle" },
    );
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <RDialog
    v-model="show"
    icon="mdi-cloud-download-outline"
    :width="mdAndUp ? 600 : '95vw'"
    cancelable
    :cancel-disabled="submitting"
    @close="close"
  >
    <template #header>
      <span>{{ t("rom.add-virtual-game") }}</span>
    </template>

    <template #content>
      <RForm class="r-v2-avg__form" @submit="submit">
        <p class="r-v2-avg__desc">{{ t("rom.add-virtual-game-desc") }}</p>

        <PlatformSelect
          v-model="platformId"
          :items="allPlatforms"
          :label="t('common.platform')"
          :placeholder="t('common.platform')"
          prefix-label="stacked"
          clearable
        />

        <RTextField
          v-model="name"
          :placeholder="t('rom.virtual-game-name')"
          prefix-label="stacked"
        >
          <template #prefix-label>{{ t("rom.virtual-game-name") }}</template>
        </RTextField>

        <div class="r-v2-avg__links">
          <div
            v-for="(row, i) in links"
            :key="i"
            class="r-v2-avg__row"
          >
            <RTextField
              v-model="row.url"
              class="r-v2-avg__url"
              placeholder="https://1fichier.com/?..."
            />
            <RSelect
              v-model="row.category"
              class="r-v2-avg__cat"
              :items="categoryItems"
              item-title="title"
              item-value="value"
            />
            <RBtn
              variant="text"
              icon="mdi-close"
              :disabled="submitting"
              @click="removeRow(i)"
            />
          </div>
          <RBtn
            variant="text"
            color="primary"
            prepend-icon="mdi-plus"
            :disabled="submitting"
            @click="addRow"
          >
            {{ t("rom.virtual-game-add-link") }}
          </RBtn>
        </div>
      </RForm>
    </template>

    <template #footer>
      <RBtn
        variant="flat"
        color="primary"
        prepend-icon="mdi-plus"
        :disabled="!canSubmit || submitting"
        :loading="submitting"
        @click="submit"
      >
        {{ t("common.add") }}
      </RBtn>
    </template>
  </RDialog>
</template>

<style scoped>
.r-v2-avg__form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.r-v2-avg__desc {
  margin: 0;
  color: var(--r-color-fg-muted);
  font-size: var(--r-font-size-sm);
}
.r-v2-avg__links {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.r-v2-avg__row {
  display: flex;
  gap: 8px;
  align-items: center;
}
.r-v2-avg__url {
  flex: 1 1 auto;
}
.r-v2-avg__cat {
  flex: 0 0 140px;
}
</style>
