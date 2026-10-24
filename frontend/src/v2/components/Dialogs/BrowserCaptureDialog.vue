<script setup lang="ts">
// BrowserCaptureDialog: shown when a virtual ROM's download needs a non-archive
// source page (captcha / Cloudflare / wait gate, as on 1fichier, gofile,
// datanodes, ...). It DISPLAYS the source page inside the dialog in an iframe so
// the check can be solved in-app; many hosts block framing (X-Frame-Options),
// so an explicit "open in a new tab" fallback is always offered, after which the
// user pastes the final file URL back and the backend fetches it. For hosts the
// server can resolve on its own (vikingfile/mediafire/filekeeper) a one-click
// "Fetch automatically" is offered instead.
import { RBtn, RDialog, RTextField } from "@v2/lib";
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import romApi from "@/services/api/rom";
import type { SimpleRom } from "@/stores/roms";
import { useEmitterEvent } from "@/v2/composables/useEmitterEvent";
import { useSnackbar } from "@/v2/composables/useSnackbar";

defineOptions({ inheritAttrs: false });

interface CapturePage {
  url: string;
  /** Server can resolve this host without a human (scrape/form). */
  auto?: boolean;
}

// `preview` force-opens the dialog with given data (for stories/tests) without
// needing the emitter.
const props = defineProps<{
  preview?: { rom: SimpleRom; pages: CapturePage[] } | undefined;
}>();

const { t } = useI18n();
const snackbar = useSnackbar();

const show = ref(false);
const rom = ref<SimpleRom | null>(null);
const pages = ref<CapturePage[]>([]);
const active = ref(0);
const finalUrl = ref("");
const submitting = ref(false);
const iframeBlocked = ref(false);

const activePage = computed<CapturePage | null>(
  () => pages.value[active.value] ?? null,
);

function openWith(payload: { rom: SimpleRom; pages: CapturePage[] }) {
  rom.value = payload.rom;
  pages.value = payload.pages ?? [];
  active.value = 0;
  finalUrl.value = "";
  iframeBlocked.value = false;
  show.value = true;
}
useEmitterEvent("showBrowserCaptureDialog", openWith);

if (props.preview) openWith(props.preview);

function hostOf(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return url;
  }
}

function selectPage(i: number) {
  active.value = i;
  finalUrl.value = "";
  iframeBlocked.value = false;
}

function closeDialog() {
  show.value = false;
  rom.value = null;
  pages.value = [];
}

function openInTab() {
  if (activePage.value) {
    window.open(activePage.value.url, "_blank", "noopener,noreferrer");
  }
}

// A cross-origin page that frames fine still can't be read, so we can't truly
// know it rendered; the fallback controls stay visible regardless. We only flip
// this when the iframe errors outright.
function onIframeError() {
  iframeBlocked.value = true;
}

async function startDownload(payload: { directUrl?: string; resolveUrl?: string }) {
  const r = rom.value;
  if (!r || submitting.value) return;
  submitting.value = true;
  try {
    await romApi.downloadVirtualRom({
      romId: r.id,
      directUrl: payload.directUrl,
      resolveUrl: payload.resolveUrl,
    });
    snackbar.success(
      t("rom.browser-capture-started", { name: r.name ?? r.fs_name }),
      { icon: "mdi-cloud-download" },
    );
    closeDialog();
  } catch {
    snackbar.error(t("rom.browser-capture-failed"), { icon: "mdi-close-circle" });
  } finally {
    submitting.value = false;
  }
}

function submitPasted() {
  const url = finalUrl.value.trim();
  if (!/^https?:\/\//i.test(url)) {
    snackbar.error(t("rom.browser-capture-bad-url"), { icon: "mdi-close-circle" });
    return;
  }
  void startDownload({ directUrl: url });
}

function fetchAutomatically() {
  if (activePage.value) void startDownload({ resolveUrl: activePage.value.url });
}
</script>

<template>
  <RDialog
    v-model="show"
    icon="mdi-shield-key-outline"
    :width="960"
    cancelable
    :cancel-text="t('common.close')"
    @close="closeDialog"
  >
    <template #header>
      <span>{{ t("rom.browser-capture-title") }}</span>
    </template>

    <template #content>
      <div class="r-v2-bcap">
        <p class="r-v2-bcap__hint">{{ t("rom.browser-capture-detail") }}</p>

        <div v-if="pages.length > 1" class="r-v2-bcap__tabs">
          <RBtn
            v-for="(p, i) in pages"
            :key="i"
            size="small"
            :variant="i === active ? 'flat' : 'translucent'"
            :color="i === active ? 'primary' : undefined"
            @click="selectPage(i)"
          >
            {{ hostOf(p.url) }}
          </RBtn>
        </div>

        <!-- The source page, rendered in-dialog. Hosts that send X-Frame-Options
             show blank here; the fallback below covers that case. -->
        <div class="r-v2-bcap__frame-wrap">
          <iframe
            v-if="activePage"
            :key="activePage.url"
            :src="activePage.url"
            class="r-v2-bcap__frame"
            title="source page"
            referrerpolicy="no-referrer"
            sandbox="allow-scripts allow-forms allow-same-origin allow-popups"
            @error="onIframeError"
          />
          <p v-if="activePage" class="r-v2-bcap__url" :title="activePage.url">
            {{ activePage.url }}
          </p>
        </div>

        <div class="r-v2-bcap__fallback">
          <span class="r-v2-bcap__note">{{
            t("rom.browser-capture-blocked-note")
          }}</span>
          <RBtn
            size="small"
            variant="translucent"
            color="primary"
            prepend-icon="mdi-open-in-new"
            @click="openInTab"
          >
            {{ t("rom.browser-capture-open") }}
          </RBtn>
          <RBtn
            v-if="activePage?.auto"
            size="small"
            variant="translucent"
            color="success"
            prepend-icon="mdi-cog-play-outline"
            :loading="submitting"
            @click="fetchAutomatically"
          >
            {{ t("rom.browser-capture-auto") }}
          </RBtn>
        </div>

        <div class="r-v2-bcap__paste">
          <RTextField
            v-model="finalUrl"
            :label="t('rom.browser-capture-url-label')"
            :hint="t('rom.browser-capture-url-hint')"
            persistent-hint
            @keyup.enter="submitPasted"
          />
        </div>
      </div>
    </template>

    <template #footer>
      <RBtn
        variant="translucent"
        color="primary"
        prepend-icon="mdi-cloud-download-outline"
        :disabled="!finalUrl.trim()"
        :loading="submitting"
        @click="submitPasted"
      >
        {{ t("rom.browser-capture-download") }}
      </RBtn>
    </template>
  </RDialog>
</template>

<style scoped>
.r-v2-bcap {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 4px 4px 0;
}
.r-v2-bcap__hint {
  margin: 0;
  font-size: var(--r-font-size-sm);
  color: var(--r-color-fg-secondary);
}
.r-v2-bcap__tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.r-v2-bcap__frame-wrap {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.r-v2-bcap__frame {
  width: 100%;
  height: 420px;
  border: 1px solid var(--r-color-border);
  border-radius: var(--r-radius-md);
  background: var(--r-color-bg-elevated);
}
.r-v2-bcap__url {
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: var(--r-font-family-mono, monospace);
  font-size: var(--r-font-size-xs);
  color: var(--r-color-fg-secondary);
}
.r-v2-bcap__fallback {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
.r-v2-bcap__note {
  flex: 1 1 auto;
  min-width: 200px;
  font-size: var(--r-font-size-xs);
  color: var(--r-color-fg-muted);
}
</style>
