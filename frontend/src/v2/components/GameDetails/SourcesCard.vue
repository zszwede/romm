<script setup lang="ts">
// SourcesCard: shows the download sources backing a ROM — its primary
// archive.org URL plus any mirrors other sources contributed (rom.source_urls,
// deduped primary-first). A game two sources list, or one already in the
// library, keeps every link here rather than losing the duplicate. Each entry
// can be removed; removing the primary promotes the next mirror server-side.
// Rendered only when the ROM actually has sources (virtual games, or a local
// file that absorbed archive.org mirrors through a merge).
import { RBtn, RIcon } from "@v2/lib";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import romApi from "@/services/api/rom";
import virtualSourceApi from "@/services/api/virtualSource";
import storeRoms, { type DetailedRom } from "@/stores/roms";
import { useCan } from "@/v2/composables/useCan";
import { useSnackbar } from "@/v2/composables/useSnackbar";

const props = defineProps<{ rom: DetailedRom }>();

const { t } = useI18n();
const snackbar = useSnackbar();
const romsStore = storeRoms();
const canEdit = useCan("rom.edit");

const sources = computed(() => props.rom.source_urls ?? []);
const removing = ref<string | null>(null);

// archive.org URLs are long; show a readable tail (item id + file) so each
// row is distinguishable without wrapping the whole URL.
function label(url: string): string {
  try {
    const u = new URL(url);
    const archive = u.searchParams.get("archive");
    const tail = (archive || u.pathname).split("/").filter(Boolean).slice(-2);
    return tail.join("/") || u.host;
  } catch {
    return url;
  }
}

async function remove(url: string) {
  if (removing.value) return;
  removing.value = url;
  try {
    await virtualSourceApi.removeSource({ romId: props.rom.id, url });
    // Refetch so source_urls (and a possibly-promoted primary) stay in sync.
    const { data } = await romApi.getRom({ romId: props.rom.id });
    romsStore.cacheDetailedRom(data);
    snackbar.success(t("rom.source-removed"));
  } catch (err) {
    const e = err as {
      response?: { data?: { detail?: string } };
      message?: string;
    };
    snackbar.error(
      t("rom.source-remove-failed", {
        error: e?.response?.data?.detail || e?.message || "",
      }),
    );
  } finally {
    removing.value = null;
  }
}
</script>

<template>
  <section v-if="sources.length" class="r-v2-src">
    <header class="r-v2-src__head">
      <RIcon icon="mdi-cloud-download-outline" size="small" />
      <span class="r-v2-src__title">{{ t("rom.download-sources") }}</span>
      <span class="r-v2-src__count">{{ sources.length }}</span>
    </header>
    <ul class="r-v2-src__list">
      <li v-for="(url, i) in sources" :key="url" class="r-v2-src__item">
        <RIcon
          :icon="i === 0 ? 'mdi-star' : 'mdi-source-branch'"
          size="x-small"
          :color="i === 0 ? 'primary' : 'fg-muted'"
        />
        <a
          class="r-v2-src__link"
          :href="url"
          target="_blank"
          rel="noopener noreferrer"
          :title="url"
          >{{ label(url) }}</a
        >
        <span v-if="i === 0" class="r-v2-src__primary">{{
          t("rom.source-primary")
        }}</span>
        <RBtn
          v-if="canEdit"
          icon="mdi-close"
          variant="text"
          size="x-small"
          :loading="removing === url"
          :aria-label="t('rom.source-remove')"
          :tooltip="t('rom.source-remove')"
          @click="remove(url)"
        />
      </li>
    </ul>
  </section>
</template>

<style scoped>
.r-v2-src {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.r-v2-src__head {
  display: flex;
  align-items: center;
  gap: 6px;
}
.r-v2-src__title {
  font-size: 11px;
  font-weight: var(--r-font-weight-semibold);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--r-color-fg-muted);
}
.r-v2-src__count {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: var(--r-radius-pill);
  background: var(--r-color-bg-elevated);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
  color: var(--r-color-fg-muted);
}
.r-v2-src__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.r-v2-src__item {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.r-v2-src__link {
  color: var(--r-color-fg);
  text-decoration: none;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.r-v2-src__link:hover {
  text-decoration: underline;
}
.r-v2-src__primary {
  flex: none;
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--r-color-brand-primary);
}
.r-v2-src__item .r-btn {
  margin-left: auto;
  flex: none;
}
</style>
