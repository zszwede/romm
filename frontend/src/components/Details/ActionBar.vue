<script setup lang="ts">
import type { Emitter } from "mitt";
import { computed, inject, ref } from "vue";
import { useI18n } from "vue-i18n";
import AdminMenu from "@/components/common/Game/AdminMenu.vue";
import CopyRomDownloadLinkDialog from "@/components/common/Game/Dialog/CopyDownloadLink.vue";
import PlayBtn from "@/components/common/Game/PlayBtn.vue";
import { useGameAnimation } from "@/composables/useGameAnimation";
import romApi from "@/services/api/rom";
import storeAuth from "@/stores/auth";
import storeDownload from "@/stores/download";
import type { DetailedRom } from "@/stores/roms";
import type { Events } from "@/types/emitter";
import { getDownloadLink, isNintendoDSRom } from "@/utils";

const props = defineProps<{ rom: DetailedRom }>();
const downloadStore = storeDownload();
const emitter = inject<Emitter<Events>>("emitter");
const qrCodeIcon = ref("mdi-qrcode");
const auth = storeAuth();
const { t } = useI18n();

const isNDSRom = computed(() => {
  return isNintendoDSRom(props.rom);
});

const { boxartStyle } = useGameAnimation({
  rom: props.rom,
});

const confirmDownloadOpen = ref(false);
const materializing = ref(false);

// A virtual ROM can have more than one archive.org source (the same game listed
// by two collections is deduped into one entry with several download mirrors).
const sourceUrls = computed<string[]>(() => props.rom.source_urls ?? []);
const sourceCount = computed(() => sourceUrls.value.length);

async function confirmVirtualDownload() {
  if (materializing.value) return;
  materializing.value = true;
  try {
    await romApi.downloadVirtualRom({ romId: props.rom.id });
    emitter?.emit("snackbarShow", {
      msg: `Downloading ${props.rom.name} from archive.org…`,
      icon: "mdi-cloud-download",
      color: "green",
      timeout: 4000,
    });
    confirmDownloadOpen.value = false;
  } catch (err) {
    const e = err as { response?: { data?: { detail?: string } } };
    emitter?.emit("snackbarShow", {
      msg: `Couldn't start download: ${e?.response?.data?.detail ?? "unknown error"}`,
      icon: "mdi-close-circle",
      color: "red",
      timeout: 5000,
    });
  } finally {
    materializing.value = false;
  }
}

async function copyDownloadLink(rom: DetailedRom) {
  const downloadLink = getDownloadLink({
    rom,
    fileIDs: downloadStore.fileIDsToDownload,
  });
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(downloadLink);
    emitter?.emit("snackbarShow", {
      msg: "Download link copied to clipboard!",
      icon: "mdi-check-bold",
      color: "green",
      timeout: 2000,
    });
  } else {
    emitter?.emit("showCopyDownloadLinkDialog", downloadLink);
  }
}
</script>

<template>
  <div>
    <v-btn-group
      divided
      density="compact"
      class="d-flex flex-row"
      :class="{
        'mt-2':
          boxartStyle !== 'physical_path' || !rom.ss_metadata?.physical_path,
      }"
    >
      <v-btn
        v-if="rom.is_virtual"
        class="flex-grow-1"
        :aria-label="`Download ${rom.name} from archive.org`"
        @click="confirmDownloadOpen = true"
      >
        <v-tooltip
          activator="parent"
          location="top"
          transition="fade-transition"
          open-delay="1000"
        >
          {{ t("rom.download") }} {{ rom.name }} (archive.org)
        </v-tooltip>
        <v-icon icon="mdi-cloud-download-outline" size="large" />
      </v-btn>
      <v-btn
        v-if="rom.is_virtual && sourceCount > 1"
        class="flex-grow-0"
        :aria-label="`${sourceCount} download sources`"
        @click="confirmDownloadOpen = true"
      >
        <v-tooltip
          activator="parent"
          location="top"
          transition="fade-transition"
          open-delay="500"
        >
          {{ sourceCount }} download sources (mirrors)
        </v-tooltip>
        <v-icon icon="mdi-source-branch" start size="small" />
        {{ sourceCount }}
      </v-btn>
      <v-btn
        v-if="!rom.is_virtual"
        :disabled="downloadStore.value.includes(rom.id) || rom.missing_from_fs"
        class="flex-grow-1"
        :aria-label="`Download ${rom.name}`"
        @click="
          romApi.downloadRom({
            rom,
            fileIDs: downloadStore.fileIDsToDownload,
          })
        "
      >
        <v-tooltip
          activator="parent"
          location="top"
          transition="fade-transition"
          open-delay="1000"
        >
          {{ t("rom.download") }} {{ rom.name }}
        </v-tooltip>
        <v-icon icon="mdi-download" size="large" />
      </v-btn>
      <v-btn
        v-if="!rom.is_virtual"
        :disabled="rom.missing_from_fs"
        :aria-label="`Copy download link ${rom.name}`"
        class="flex-grow-1"
        @click="copyDownloadLink(rom)"
      >
        <v-tooltip
          activator="parent"
          location="top"
          transition="fade-transition"
          open-delay="1000"
        >
          {{ t("rom.copy-link") }}
        </v-tooltip>
        <v-icon icon="mdi-content-copy" />
      </v-btn>
      <PlayBtn v-if="!rom.is_virtual" :rom="rom" class="flex-grow-1" />
      <v-btn
        v-if="isNDSRom"
        :disabled="rom.missing_from_fs"
        class="flex-grow-1"
        :aria-label="`Show ${rom.name} QR code`"
        @click="emitter?.emit('showQRCodeDialog', rom)"
      >
        <v-icon :icon="qrCodeIcon" />
      </v-btn>
      <v-menu
        v-if="
          auth.scopes.includes('roms.write') ||
          auth.scopes.includes('roms.user.write') ||
          auth.scopes.includes('collections.write')
        "
        location="bottom"
      >
        <template #activator="{ props: menuProps }">
          <v-btn
            :aria-label="`${rom.name} admin menu`"
            class="flex-grow-1"
            v-bind="menuProps"
          >
            <v-icon icon="mdi-dots-vertical" size="large" />
          </v-btn>
        </template>
        <AdminMenu :rom="rom" />
      </v-menu>
    </v-btn-group>

    <CopyRomDownloadLinkDialog />

    <v-dialog
      v-model="confirmDownloadOpen"
      max-width="420"
      @click:outside="confirmDownloadOpen = false"
    >
      <v-card>
        <v-card-title class="text-body-1">{{ t("rom.download") }}</v-card-title>
        <v-card-text>
          Download <strong>{{ rom.name }}</strong> from archive.org? It becomes a
          normal game once the download finishes.
          <template v-if="sourceCount > 1">
            <div class="mt-3 text-caption text-medium-emphasis">
              {{ sourceCount }} sources available — they're tried in order until
              one succeeds:
            </div>
            <v-list density="compact" class="bg-transparent pa-0 mt-1">
              <v-list-item
                v-for="(url, i) in sourceUrls"
                :key="url"
                class="px-0"
                min-height="24"
              >
                <template #prepend>
                  <span class="text-caption text-medium-emphasis mr-2"
                    >{{ i + 1 }}.</span
                  >
                </template>
                <v-list-item-title
                  class="text-caption text-truncate"
                  :title="url"
                  >{{ url }}</v-list-item-title
                >
              </v-list-item>
            </v-list>
          </template>
        </v-card-text>
        <v-card-actions class="justify-end">
          <v-btn :disabled="materializing" @click="confirmDownloadOpen = false">
            {{ t("common.cancel") }}
          </v-btn>
          <v-btn
            color="primary"
            :loading="materializing"
            @click="confirmVirtualDownload"
          >
            {{ t("rom.download") }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>
