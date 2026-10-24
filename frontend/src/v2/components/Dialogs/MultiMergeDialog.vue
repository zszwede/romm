<script setup lang="ts">
// MultiMergeDialog: search a single platform's ROMs (UNGROUPED, so duplicate
// entries of one game show as separate rows), tick the ones that are the same
// game, and merge them into one via POST /virtual/merge. The backend keeps a
// single keeper (a local/physical copy wins, else the identified one, else the
// lowest id) and folds every other (virtual) entry's download URLs into it as
// mirrors — so nothing on disk is lost.
//
// Opened from a platform (empty search) or from a game page (search pre-filled
// with that game's name, and exact same-game matches auto-selected using the
// alpha-numeric title normalization the merge/match scripts use).
import { RBtn, RDialog, RForm, RIcon, RTextField } from "@v2/lib";
import { computed, nextTick, ref } from "vue";
import { useI18n } from "vue-i18n";
import romApi from "@/services/api/rom";
import virtualSourceApi from "@/services/api/virtualSource";
import type { SimpleRom } from "@/stores/roms";
import { useBreakpoint } from "@/v2/composables/useBreakpoint";
import { useEmitterEvent } from "@/v2/composables/useEmitterEvent";
import { useRomSync } from "@/v2/composables/useRomSync";
import { useSnackbar } from "@/v2/composables/useSnackbar";
import storeGalleryRoms from "@/v2/stores/galleryRoms";

defineOptions({ inheritAttrs: false });

const { t } = useI18n();
const { mdAndUp } = useBreakpoint();
const snackbar = useSnackbar();
const galleryRoms = storeGalleryRoms();
const { removeCachedRoms } = useRomSync();

const show = ref(false);
const platformId = ref<number | null>(null);
const searchText = ref("");
const results = ref<SimpleRom[]>([]);
const selectedIds = ref<Set<number>>(new Set());
const searching = ref(false);
const merging = ref(false);
const searched = ref(false);

// Alpha-numeric title normalization: strip (parenthesised)/[bracketed] tags,
// lowercase, collapse every non-[a-z0-9] run to a single space. Mirrors the
// backend scripts' normalize_title so "Fire Emblem: Three Houses (USA)" and
// "FIRE EMBLEM - THREE HOUSES" both fold to "fire emblem three houses".
function normalizeTitle(raw: string): string {
  return (raw || "")
    .replace(/\([^)]*\)|\[[^\]]*\]/g, " ")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, " ")
    .trim();
}

function rowLabel(rom: SimpleRom): string {
  return rom.fs_name_no_ext || rom.name || `#${rom.id}`;
}

const openHandler = (payload: { platformId: number | null; search?: string }) => {
  platformId.value = payload.platformId;
  searchText.value = payload.search ?? "";
  results.value = [];
  selectedIds.value = new Set();
  searched.value = false;
  show.value = true;
  // A seeded search (opened from a game) runs immediately and auto-selects the
  // same-game entries.
  if (searchText.value.trim()) {
    void nextTick(() => search({ autoSelect: true }));
  }
};
useEmitterEvent("showMultiMergeDialog", openHandler);

function close() {
  show.value = false;
}

function toggle(id: number) {
  const next = new Set(selectedIds.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selectedIds.value = next;
}

async function search({ autoSelect = false }: { autoSelect?: boolean } = {}) {
  if (searching.value) return;
  const term = searchText.value.trim();
  if (!term) return;
  searching.value = true;
  try {
    const { data } = await romApi.getRoms({
      platformIds: platformId.value != null ? [platformId.value] : null,
      searchTerm: term,
      // Ungrouped, so every duplicate/version is its own selectable row.
      groupByMetaId: false,
      limit: 250,
      orderBy: "name",
      orderDir: "asc",
      withTotal: false,
      withCharIndex: false,
      withFilterValues: false,
    });
    results.value = data.items ?? [];
    searched.value = true;
    if (autoSelect) {
      const want = normalizeTitle(term);
      selectedIds.value = new Set(
        results.value
          .filter((r) => normalizeTitle(rowLabel(r)) === want)
          .map((r) => r.id),
      );
    }
  } catch (err) {
    const e = err as { response?: { data?: { detail?: string } }; message?: string };
    snackbar.error(
      `${t("rom.multi-merge-search-failed")}: ${e?.response?.data?.detail || e?.message || ""}`,
      { icon: "mdi-close-circle" },
    );
  } finally {
    searching.value = false;
  }
}

const selectedRoms = computed(() =>
  results.value.filter((r) => selectedIds.value.has(r.id)),
);

// Deterministic keeper, mirrors the backend: physical first, then identified,
// then lowest id.
const keeper = computed<SimpleRom | null>(() => {
  const roms = selectedRoms.value;
  if (roms.length === 0) return null;
  return [...roms].sort((a, b) => {
    if (a.is_virtual !== b.is_virtual) return a.is_virtual ? 1 : -1;
    if (a.is_identified !== b.is_identified) return a.is_identified ? -1 : 1;
    return a.id - b.id;
  })[0]!;
});

const physicalCount = computed(
  () => selectedRoms.value.filter((r) => !r.is_virtual).length,
);
// At most one local copy may take part (a physical can't be absorbed without
// losing its file).
const tooManyPhysical = computed(() => physicalCount.value > 1);
const canMerge = computed(
  () =>
    !merging.value &&
    selectedRoms.value.length >= 2 &&
    !tooManyPhysical.value &&
    keeper.value != null,
);

async function merge() {
  if (!canMerge.value || keeper.value == null) return;
  const keep = keeper.value;
  const absorbed = selectedRoms.value.filter((r) => r.id !== keep.id);
  merging.value = true;
  try {
    await virtualSourceApi.merge({ romIds: [keep.id, ...absorbed.map((r) => r.id)] });
    removeCachedRoms(absorbed);
    galleryRoms.invalidateWindows();
    void galleryRoms.fetchInitialMetadata();
    snackbar.success(t("gallery.merge-success"));
    show.value = false;
  } catch (err) {
    const e = err as { response?: { data?: { detail?: string } }; message?: string };
    snackbar.error(
      t("gallery.merge-fail", {
        error: e?.response?.data?.detail || e?.message || "",
      }),
    );
  } finally {
    merging.value = false;
  }
}
</script>

<template>
  <RDialog
    v-model="show"
    icon="mdi-call-merge"
    :width="mdAndUp ? 680 : '95vw'"
    cancelable
    :cancel-disabled="merging"
    @close="close"
  >
    <template #header>
      <span>{{ t("rom.multi-merge-title") }}</span>
    </template>

    <template #content>
      <div class="r-v2-mm">
        <p class="r-v2-mm__desc">{{ t("rom.multi-merge-desc") }}</p>

        <RForm class="r-v2-mm__search" @submit="search()">
          <RTextField
            v-model="searchText"
            class="r-v2-mm__search-field"
            :placeholder="t('rom.multi-merge-search-placeholder')"
            autofocus
            @keydown.enter.prevent="search()"
          />
          <RBtn
            variant="flat"
            color="primary"
            prepend-icon="mdi-magnify"
            :disabled="!searchText.trim() || searching"
            :loading="searching"
            @click="search()"
          >
            {{ t("common.search") }}
          </RBtn>
        </RForm>

        <p
          v-if="searched && results.length === 0"
          class="r-v2-mm__empty"
        >
          {{ t("rom.multi-merge-no-results") }}
        </p>

        <ul v-if="results.length" class="r-v2-mm__list">
          <li v-for="rom in results" :key="rom.id">
            <button
              type="button"
              class="r-v2-mm__row"
              :class="{ 'r-v2-mm__row--on': selectedIds.has(rom.id) }"
              :aria-pressed="selectedIds.has(rom.id)"
              @click="toggle(rom.id)"
            >
              <RIcon
                :icon="
                  selectedIds.has(rom.id)
                    ? 'mdi-checkbox-marked'
                    : 'mdi-checkbox-blank-outline'
                "
                size="18"
                class="r-v2-mm__check"
              />
              <RIcon
                :icon="rom.is_virtual ? 'mdi-cloud-outline' : 'mdi-disc'"
                size="16"
                class="r-v2-mm__kind"
                :aria-label="
                  rom.is_virtual
                    ? t('rom.multi-merge-virtual')
                    : t('rom.multi-merge-local')
                "
              />
              <span class="r-v2-mm__name">{{ rowLabel(rom) }}</span>
              <span
                v-if="keeper && keeper.id === rom.id"
                class="r-v2-mm__badge"
              >{{ t("rom.multi-merge-keeper-badge") }}</span>
            </button>
          </li>
        </ul>
      </div>
    </template>

    <template #footer>
      <div class="r-v2-mm__footer">
        <span v-if="tooManyPhysical" class="r-v2-mm__warn">
          {{ t("rom.multi-merge-too-many-physical") }}
        </span>
        <span
          v-else-if="keeper && selectedRoms.length >= 2"
          class="r-v2-mm__summary"
        >
          {{
            t("rom.multi-merge-summary", {
              name: rowLabel(keeper),
              n: selectedRoms.length - 1,
            })
          }}
        </span>
        <span v-else class="r-v2-mm__summary">
          {{ t("rom.multi-merge-select-hint") }}
        </span>
        <RBtn
          variant="flat"
          color="primary"
          prepend-icon="mdi-call-merge"
          :disabled="!canMerge"
          :loading="merging"
          @click="merge"
        >
          {{ t("gallery.selection-merge") }}
        </RBtn>
      </div>
    </template>
  </RDialog>
</template>

<style scoped>
.r-v2-mm {
  display: flex;
  flex-direction: column;
  gap: 14px;
}
.r-v2-mm__desc {
  margin: 0;
  color: var(--r-color-fg-muted);
  font-size: var(--r-font-size-sm);
}
.r-v2-mm__search {
  display: flex;
  gap: 8px;
  align-items: center;
}
.r-v2-mm__search-field {
  flex: 1 1 auto;
}
.r-v2-mm__empty {
  margin: 0;
  color: var(--r-color-fg-faint);
  font-size: var(--r-font-size-sm);
}
.r-v2-mm__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 46vh;
  overflow-y: auto;
}
.r-v2-mm__row {
  appearance: none;
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border: 1px solid var(--r-color-border-subtle);
  border-radius: var(--r-radius-md);
  background: transparent;
  color: var(--r-color-fg-primary);
  cursor: pointer;
  text-align: left;
  transition:
    background var(--r-motion-fast) var(--r-motion-ease-out),
    border-color var(--r-motion-fast) var(--r-motion-ease-out);
}
.r-v2-mm__row:hover {
  background: var(--r-color-surface-hover);
}
.r-v2-mm__row--on {
  border-color: color-mix(in srgb, var(--r-color-brand-accent) 55%, transparent);
  background: color-mix(in srgb, var(--r-color-brand-accent) 10%, transparent);
}
.r-v2-mm__check {
  color: var(--r-color-brand-accent);
  flex: 0 0 auto;
}
.r-v2-mm__kind {
  color: var(--r-color-fg-faint);
  flex: 0 0 auto;
}
.r-v2-mm__name {
  flex: 1 1 auto;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.r-v2-mm__badge {
  flex: 0 0 auto;
  font-size: 10px;
  font-weight: var(--r-font-weight-bold);
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--r-color-brand-accent);
  border: 1px solid
    color-mix(in srgb, var(--r-color-brand-accent) 45%, transparent);
  border-radius: var(--r-radius-pill);
  padding: 2px 8px;
}
.r-v2-mm__footer {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
}
.r-v2-mm__summary {
  flex: 1 1 auto;
  color: var(--r-color-fg-muted);
  font-size: var(--r-font-size-sm);
}
.r-v2-mm__warn {
  flex: 1 1 auto;
  color: var(--r-color-danger);
  font-size: var(--r-font-size-sm);
}
</style>
