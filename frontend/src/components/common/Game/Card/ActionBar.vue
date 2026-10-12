<script setup lang="ts">
import type { Emitter } from "mitt";
import { computed, inject, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AdminMenu from "@/components/common/Game/AdminMenu.vue";
import PlayBtn from "@/components/common/Game/PlayBtn.vue";
import romApi from "@/services/api/rom";
import storeAuth from "@/stores/auth";
import storeConfig from "@/stores/config";
import storeDownload from "@/stores/download";
import storeHeartbeat from "@/stores/heartbeat";
import type { SimpleRom } from "@/stores/roms";
import type { Events } from "@/types/emitter";
import { isEJSEmulationSupported, isRuffleEmulationSupported } from "@/utils";
import { useCan } from "@/v2/composables/useCan";

const props = defineProps<{ rom: SimpleRom; sizeActionBar: number }>();
const { t } = useI18n();
const emit = defineEmits(["menu-open", "menu-close"]);
const emitter = inject<Emitter<Events>>("emitter");
const downloadStore = storeDownload();
const auth = storeAuth();
const configStore = storeConfig();
const heartbeatStore = storeHeartbeat();

const confirmDownloadOpen = ref(false);
const materializing = ref(false);

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

const computedSize = computed(() => {
  return props.sizeActionBar === 1 ? "small" : "x-small";
});

const isAprilFools = computed(() => {
  const today = new Date();
  return today.getMonth() === 3 && today.getDate() === 1;
});

const canPlayInBrowser = useCan("rom.play");
const isEmulationSupported = computed(() => {
  if (!canPlayInBrowser.value) return false;
  return (
    isEJSEmulationSupported(
      props.rom.platform_slug,
      heartbeatStore.value,
      configStore.config,
    ) ||
    isRuffleEmulationSupported(
      props.rom.platform_slug,
      heartbeatStore.value,
      configStore.config,
    )
  );
});

const menuOpen = ref(false);

watch(menuOpen, (val) => {
  emit(val ? "menu-open" : "menu-close");
});
</script>

<template>
  <v-row no-gutters>
    <v-col v-if="rom.is_virtual" class="d-flex">
      <v-btn
        class="action-bar-btn-small flex-grow-1"
        :size="computedSize"
        icon="mdi-cloud-download-outline"
        variant="text"
        rounded="0"
        :aria-label="`${t('rom.download')} ${rom.name}`"
        @click.prevent="confirmDownloadOpen = true"
      />
    </v-col>
    <v-col v-else class="d-flex">
      <v-btn
        class="action-bar-btn-small flex-grow-1"
        :size="computedSize"
        :disabled="downloadStore.value.includes(rom.id) || rom.missing_from_fs"
        icon="mdi-download"
        variant="text"
        rounded="0"
        :aria-label="`${t('rom.download')} ${rom.name}`"
        @click.prevent="romApi.downloadRom({ rom })"
      />
    </v-col>
    <v-col
      v-if="!rom.is_virtual && (isEmulationSupported || isAprilFools)"
      class="d-flex"
    >
      <PlayBtn
        :rom="rom"
        icon-embedded
        class="action-bar-btn-small flex-grow-1"
        :size="computedSize"
        variant="text"
        rounded="0"
        @click.prevent
      />
    </v-col>
    <v-col
      v-if="
        auth.scopes.includes('roms.write') ||
        auth.scopes.includes('roms.user.write') ||
        auth.scopes.includes('collections.write')
      "
      class="d-flex"
    >
      <v-menu v-model="menuOpen" location="bottom">
        <template #activator="{ props: menuProps }">
          <v-btn
            class="action-bar-btn-small flex-grow-1"
            :size="computedSize"
            v-bind="menuProps"
            icon="mdi-dots-vertical"
            variant="text"
            rounded="0"
            :aria-label="`${rom.name} admin menu`"
            @click.prevent
          />
        </template>
        <AdminMenu :rom="rom" />
      </v-menu>
    </v-col>
  </v-row>

  <v-dialog v-model="confirmDownloadOpen" max-width="420" @click:outside="confirmDownloadOpen = false">
    <v-card>
      <v-card-title class="text-body-1">{{ t("rom.download") }}</v-card-title>
      <v-card-text>
        Download <strong>{{ rom.name }}</strong> from archive.org? It becomes a
        normal game once the download finishes.
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
</template>

<style scoped>
.action-bar-btn-small {
  max-height: 250px;
  width: unset;
}
</style>
