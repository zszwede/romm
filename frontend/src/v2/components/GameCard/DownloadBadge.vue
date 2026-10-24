<script setup lang="ts">
// DownloadBadge: a small top-left pill on a virtual game's cover showing where
// it sits in the download pipeline (wanted / queued / running / failed). Shown
// only for virtual games that carry a download_status; a game that finished
// downloading is no longer virtual, so `done` never renders here. Purely
// informational — pointer events pass through to the card.
import { RIcon } from "@v2/lib";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { SimpleRom } from "@/stores/roms";

const props = defineProps<{ rom: SimpleRom }>();
const { t } = useI18n();

const STATUS = {
  wanted: { icon: "mdi-download-outline", color: "warning" },
  queued: { icon: "mdi-cloud-clock-outline", color: "info" },
  running: { icon: "mdi-cloud-sync-outline", color: "info" },
  failed: { icon: "mdi-alert-circle-outline", color: "danger" },
} as const;

const state = computed(() => {
  if (!props.rom.is_virtual) return null;
  const status = props.rom.download_status as keyof typeof STATUS | null;
  return status && status in STATUS ? STATUS[status] : null;
});

const label = computed(() =>
  props.rom.download_status
    ? t(`rom.download-status-${props.rom.download_status}`)
    : "",
);
</script>

<template>
  <span v-if="state" class="download-badge" :title="label">
    <RIcon :icon="state.icon" :color="state.color" size="14" />
  </span>
</template>

<style scoped>
.download-badge {
  position: absolute;
  top: 7px;
  left: 7px;
  z-index: 2;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 3px;
  background: var(--r-color-overlay-scrim-soft);
  border: 1px solid var(--r-color-overlay-border);
  border-radius: var(--r-radius-pill);
  pointer-events: none;
  backdrop-filter: blur(6px);
}
</style>
