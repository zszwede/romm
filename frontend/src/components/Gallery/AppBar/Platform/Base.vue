<script setup lang="ts">
import type { Emitter } from "mitt";
import { storeToRefs } from "pinia";
import { computed, inject } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import BaseGalleryAppBar from "@/components/Gallery/AppBar/Base.vue";
import FirmwareBtn from "@/components/Gallery/AppBar/Platform/FirmwareBtn.vue";
import FirmwareDrawer from "@/components/Gallery/AppBar/Platform/FirmwareDrawer.vue";
import PlatformInfoDrawer from "@/components/Gallery/AppBar/Platform/PlatformInfoDrawer.vue";
import AddVirtualSource from "@/components/common/Platform/Dialog/AddVirtualSource.vue";
import MissingFromFSIcon from "@/components/common/MissingFromFSIcon.vue";
import PlatformIcon from "@/components/common/Platform/PlatformIcon.vue";
import { userStorage } from "@/composables/useUserLocalStorage";
import storeAuth from "@/stores/auth";
import storeNavigation from "@/stores/navigation";
import storeRoms from "@/stores/roms";
import type { Events } from "@/types/emitter";

const { t } = useI18n();
const { xs } = useDisplay();
const emitter = inject<Emitter<Events>>("emitter");
const romsStore = storeRoms();
const navigationStore = storeNavigation();
const auth = storeAuth();
const { currentPlatform } = storeToRefs(romsStore);
const { activePlatformInfoDrawer } = storeToRefs(navigationStore);

// Per-platform show/hide of virtual (archive.org, not-yet-downloaded) games.
// Persisted per user+platform; a change re-runs the gallery filter pipeline.
const showVirtual = computed({
  get() {
    const id = currentPlatform.value?.id;
    if (id == null) return true;
    return userStorage.getItem(`platform.${id}.showVirtual`) !== "false";
  },
  set(value: boolean) {
    const id = currentPlatform.value?.id;
    if (id == null) return;
    userStorage.setItem(`platform.${id}.showVirtual`, String(value));
    emitter?.emit("filterRoms", null);
  },
});

function openAddVirtualSource() {
  emitter?.emit("showAddVirtualSourceDialog", currentPlatform.value);
}
</script>

<template>
  <BaseGalleryAppBar :show-playables-filter="false" :show-search-bar="!xs">
    <template #prepend>
      <MissingFromFSIcon
        v-if="currentPlatform && currentPlatform.missing_from_fs"
        text="Missing platform from filesystem"
        class="mx-2"
        :size="18"
      />
      <v-btn
        v-if="currentPlatform"
        variant="text"
        rounded="0"
        icon="mdi-cog"
        :color="activePlatformInfoDrawer ? 'primary' : ''"
        @click="navigationStore.switchActivePlatformInfoDrawer"
      >
        <PlatformIcon
          :slug="currentPlatform.slug"
          :name="currentPlatform.name"
          :fs-slug="currentPlatform.fs_slug"
          :size="36"
          class="mx-3"
        />
        <v-icon
          icon="mdi-cog"
          size="xs"
          class="position-absolute"
          style="bottom: 4px; right: 4px"
        />
      </v-btn>
      <FirmwareBtn />
      <v-btn
        v-if="currentPlatform"
        variant="text"
        rounded="0"
        :icon="showVirtual ? 'mdi-cloud' : 'mdi-cloud-off-outline'"
        :color="showVirtual ? 'primary' : ''"
        :title="showVirtual ? t('platform.hide-virtual') : t('platform.show-virtual')"
        @click="showVirtual = !showVirtual"
      />
      <v-btn
        v-if="currentPlatform && auth.scopes.includes('platforms.write')"
        variant="text"
        rounded="0"
        icon="mdi-cloud-download-outline"
        :title="t('platform.add-virtual-source')"
        @click="openAddVirtualSource"
      />
    </template>
  </BaseGalleryAppBar>

  <FirmwareDrawer />
  <PlatformInfoDrawer />
  <AddVirtualSource />
</template>

<style scoped>
.platform-icon {
  transition:
    filter 0.15s ease-in-out,
    transform 0.15s ease-in-out;
  filter: drop-shadow(0px 0px 1px rgba(var(--v-theme-primary)));
}

.platform-icon:hover,
.platform-icon.active {
  filter: drop-shadow(0px 0px 3px rgba(var(--v-theme-primary)));
  transform: scale(1.1);
}
</style>
