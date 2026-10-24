<script setup lang="ts">
// ContentsCard: the downloadable bundle of a virtual game — its base game
// (editions like NSP/XCI, each possibly multi-part), updates and DLC, read from
// rom.virtual_contents. The user picks ONE base edition, ONE update (or none)
// and ANY number of DLC, then "Download selection" dispatches exactly those
// links to the merge service, which merges them into one file. Multi-part files
// share a group and are picked/fetched together. Rendered only for a virtual
// game that has a captured content set.
import { RBtn, RIcon } from "@v2/lib";
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import romApi from "@/services/api/rom";
import storeRoms, { type DetailedRom } from "@/stores/roms";
import { useCan } from "@/v2/composables/useCan";
import { useSnackbar } from "@/v2/composables/useSnackbar";

interface Content {
  category?: string | null;
  group?: string | null;
  label?: string | null;
  format?: string | null;
  part?: number | null;
  name?: string | null;
  url?: string | null;
  host?: string | null;
  version?: string | null;
  size?: number | null;
}
interface Unit {
  group: string;
  category: string;
  label: string;
  host: string | null;
  size: number;
  parts: number;
  urls: string[];
}

const props = defineProps<{ rom: DetailedRom }>();
const { t } = useI18n();
const snackbar = useSnackbar();
const romsStore = storeRoms();
const canDownload = useCan("rom.edit");

const contents = computed<Content[]>(
  () =>
    (props.rom as { virtual_contents?: Content[] | null }).virtual_contents ??
    [],
);

const BASE = new Set(["base", "base_game", "game", "", null as unknown as string]);

function unitsOf(cats: (c: Content) => boolean): Unit[] {
  const by = new Map<string, Unit>();
  for (const c of contents.value) {
    if (!c.url || !cats(c)) continue;
    const g = c.group ?? c.url;
    let u = by.get(g);
    if (!u) {
      u = {
        group: g,
        category: (c.category ?? "base") as string,
        label: c.label || c.format || t("rom.contents-base"),
        host: c.host ?? null,
        size: 0,
        parts: 0,
        urls: [],
      };
      by.set(g, u);
    }
    u.urls.push(c.url);
    u.parts += 1;
    u.size += c.size ?? 0;
  }
  return [...by.values()];
}

const baseUnits = computed(() => unitsOf((c) => BASE.has((c.category ?? "") as string)));
const updateUnits = computed(() =>
  unitsOf((c) => c.category === "update" || c.category === "patch"),
);
const dlcUnits = computed(() => unitsOf((c) => c.category === "dlc"));

const selBase = ref<string>("");
const selUpdate = ref<string>(""); // "" = no update
const selDlc = ref<string[]>([]);

// Sensible default: first base edition, first (latest-listed) update, all DLC.
watch(
  contents,
  () => {
    selBase.value = baseUnits.value[0]?.group ?? "";
    selUpdate.value = updateUnits.value[0]?.group ?? "";
    selDlc.value = dlcUnits.value.map((u) => u.group);
  },
  { immediate: true },
);

function toggleDlc(g: string) {
  const i = selDlc.value.indexOf(g);
  if (i >= 0) selDlc.value.splice(i, 1);
  else selDlc.value.push(g);
}

const selectedUrls = computed<string[]>(() => {
  const urls: string[] = [];
  const push = (units: Unit[], picked: string | string[]) => {
    const want = Array.isArray(picked) ? new Set(picked) : new Set([picked]);
    for (const u of units) if (want.has(u.group)) urls.push(...u.urls);
  };
  push(baseUnits.value, selBase.value);
  if (selUpdate.value) push(updateUnits.value, selUpdate.value);
  push(dlcUnits.value, selDlc.value);
  return urls;
});

const totalSize = computed(() => {
  let n = 0;
  const pick = (units: Unit[], want: Set<string>) => {
    for (const u of units) if (want.has(u.group)) n += u.size;
  };
  pick(baseUnits.value, new Set([selBase.value]));
  if (selUpdate.value) pick(updateUnits.value, new Set([selUpdate.value]));
  pick(dlcUnits.value, new Set(selDlc.value));
  return n;
});

function fmtSize(bytes: number): string {
  if (!bytes || bytes <= 0) return "";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024;
    i += 1;
  }
  return `${n >= 10 || i === 0 ? Math.round(n) : n.toFixed(1)} ${u[i]}`;
}

function unitLabel(u: Unit): string {
  const parts = u.parts > 1 ? ` · ${t("rom.contents-parts", { n: u.parts })}` : "";
  const size = u.size ? ` · ${fmtSize(u.size)}` : "";
  return `${u.label}${parts}${size}`;
}

const dispatching = ref(false);
async function downloadSelection() {
  if (dispatching.value || !selectedUrls.value.length) return;
  dispatching.value = true;
  try {
    const { data } = await romApi.dispatchVirtualRom({
      romId: props.rom.id,
      urls: selectedUrls.value,
    });
    props.rom.download_status = data.status;
    romsStore.cacheDetailedRom(props.rom);
    snackbar.success(t("rom.download-dispatched"), { icon: "mdi-cloud-upload" });
  } catch {
    snackbar.error(t("rom.download-dispatch-failed"), { icon: "mdi-close-circle" });
  } finally {
    dispatching.value = false;
  }
}
</script>

<template>
  <section v-if="contents.length" class="r-v2-contents">
    <header class="r-v2-contents__head">
      <RIcon icon="mdi-package-variant-closed" size="small" />
      <span class="r-v2-contents__title">{{ t("rom.contents-title") }}</span>
    </header>

    <!-- Base: pick one edition -->
    <div v-if="baseUnits.length" class="r-v2-contents__group">
      <div class="r-v2-contents__group-head">{{ t("rom.contents-group-base") }}</div>
      <label v-for="u in baseUnits" :key="u.group" class="r-v2-contents__row">
        <input
          type="radio"
          name="base"
          :value="u.group"
          :checked="selBase === u.group"
          @change="selBase = u.group"
        />
        <span class="r-v2-contents__label">{{ unitLabel(u) }}</span>
        <span v-if="u.host" class="r-v2-contents__host">{{ u.host }}</span>
      </label>
    </div>

    <!-- Update: pick one, or none -->
    <div v-if="updateUnits.length" class="r-v2-contents__group">
      <div class="r-v2-contents__group-head">{{ t("rom.contents-group-update") }}</div>
      <label
        v-for="u in updateUnits"
        :key="u.group"
        class="r-v2-contents__row"
      >
        <input
          type="radio"
          name="update"
          :value="u.group"
          :checked="selUpdate === u.group"
          @change="selUpdate = u.group"
        />
        <span class="r-v2-contents__label">{{ unitLabel(u) }}</span>
        <span v-if="u.host" class="r-v2-contents__host">{{ u.host }}</span>
      </label>
      <label class="r-v2-contents__row">
        <input
          type="radio"
          name="update"
          value=""
          :checked="selUpdate === ''"
          @change="selUpdate = ''"
        />
        <span class="r-v2-contents__label r-v2-contents__muted">{{
          t("rom.contents-no-update")
        }}</span>
      </label>
    </div>

    <!-- DLC: pick any -->
    <div v-if="dlcUnits.length" class="r-v2-contents__group">
      <div class="r-v2-contents__group-head">{{ t("rom.contents-group-dlc") }}</div>
      <label v-for="u in dlcUnits" :key="u.group" class="r-v2-contents__row">
        <input
          type="checkbox"
          :checked="selDlc.includes(u.group)"
          @change="toggleDlc(u.group)"
        />
        <span class="r-v2-contents__label">{{ unitLabel(u) }}</span>
        <span v-if="u.host" class="r-v2-contents__host">{{ u.host }}</span>
      </label>
    </div>

    <div class="r-v2-contents__foot">
      <RBtn
        v-if="canDownload"
        variant="outlined"
        prepend-icon="mdi-cloud-download"
        :loading="dispatching"
        :disabled="!selectedUrls.length"
        @click="downloadSelection"
      >
        {{ t("rom.contents-download-selection") }}
      </RBtn>
      <span class="r-v2-contents__total">
        {{ selectedUrls.length }} {{ t("rom.contents-files") }}
        <template v-if="totalSize">· {{ fmtSize(totalSize) }}</template>
      </span>
    </div>
  </section>
</template>

<style scoped>
.r-v2-contents {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.r-v2-contents__head {
  display: flex;
  align-items: center;
  gap: 6px;
}
.r-v2-contents__title {
  font-size: 11px;
  font-weight: var(--r-font-weight-semibold);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--r-color-fg-muted);
}
.r-v2-contents__group {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.r-v2-contents__group-head {
  font-size: 12px;
  font-weight: var(--r-font-weight-semibold);
  color: var(--r-color-fg-secondary);
}
.r-v2-contents__row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 13px;
  cursor: pointer;
  padding-left: 4px;
}
.r-v2-contents__row input {
  flex: none;
  accent-color: var(--r-color-brand-primary);
}
.r-v2-contents__label {
  color: var(--r-color-fg);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.r-v2-contents__muted {
  color: var(--r-color-fg-faint);
}
.r-v2-contents__host {
  flex: none;
  margin-left: auto;
  font-size: 10px;
  padding: 1px 6px;
  border-radius: var(--r-radius-pill);
  background: var(--r-color-bg-elevated);
  color: var(--r-color-fg-muted);
}
.r-v2-contents__foot {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 2px;
}
.r-v2-contents__total {
  font-size: 11px;
  color: var(--r-color-fg-faint);
  font-variant-numeric: tabular-nums;
}
</style>
