// useGalleryNeighbors: resolve the previous/next game and navigate to them.
//
// Primary order is the gallery store's `romIdIndex` (search term, filters,
// order-by and grouping baked in) — used when the user actually walked in from
// that gallery (useGalleryProvenance), so a ROM opened from Home/Activity/scan
// doesn't inherit a stale list.
//
// Fallback: when there is no live gallery context (a refreshed or
// directly-loaded game page), step through the ROM's platform in the DEFAULT
// (alphabetical / name-asc) order instead, so the arrows always work. The
// default-order id index is fetched once per platform and cached module-level
// so it survives the component remounts that happen while stepping.
//
// Lifted out of PrevNextNav.vue so the on-screen arrows and the
// ArrowLeft/Right keyboard shortcuts share one source of truth.
import { computed, type MaybeRefOrGetter, ref, toValue, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ROUTES } from "@/plugins/router";
import romApi from "@/services/api/rom";
import storeRoms from "@/stores/roms";
import { useGalleryProvenance } from "@/v2/composables/useGalleryProvenance";
import storeGalleryRoms, { DEFAULT_ORDER_BY } from "@/v2/stores/galleryRoms";

// Platform -> default-order (name asc) rom id index. Shared + persistent so
// stepping from rom to rom reuses it without re-fetching.
const fallbackIndex = ref<{ platformId: number; ids: number[] } | null>(null);
let fallbackInFlight: number | null = null;

async function ensureFallbackIndex(platformId: number): Promise<void> {
  if (fallbackIndex.value?.platformId === platformId) return;
  if (fallbackInFlight === platformId) return;
  fallbackInFlight = platformId;
  try {
    const { data } = await romApi.getRoms({
      platformIds: [platformId],
      orderBy: DEFAULT_ORDER_BY,
      orderDir: "asc",
      limit: 1,
      withRomIdIndex: true,
      withTotal: false,
      withCharIndex: false,
      withFilterValues: false,
    });
    fallbackIndex.value = { platformId, ids: data.rom_id_index ?? [] };
  } catch {
    // Leave whatever we had; stepping just won't be available this time.
  } finally {
    if (fallbackInFlight === platformId) fallbackInFlight = null;
  }
}

export function useGalleryNeighbors(romId: MaybeRefOrGetter<number>) {
  const route = useRoute();
  const router = useRouter();
  const galleryRoms = storeGalleryRoms();
  const romsStore = storeRoms();
  const { enteredFromGallery } = useGalleryProvenance();

  // The ROM's platform, read off the detail page's current rom.
  const platformId = computed(() => {
    const cur = romsStore.currentRom;
    return cur && cur.id === toValue(romId) ? cur.platform_id : null;
  });

  // Are we genuinely stepping the gallery the user came from?
  const inGallery = computed(
    () =>
      enteredFromGallery.value &&
      galleryRoms.romIdIndex.indexOf(toValue(romId)) >= 0,
  );

  // Load the default-order fallback whenever we're NOT in a live gallery walk
  // and the current ROM isn't already covered by the cached index. (Stepping to
  // the next game lands on a ROM already in the cache, so no reload — and the
  // index stays available even before `currentRom` catches up to the new id.)
  watch(
    [inGallery, platformId, () => toValue(romId)],
    ([ing, pid, id]) => {
      if (ing || pid == null) return;
      if (fallbackIndex.value?.ids.includes(id)) return;
      void ensureFallbackIndex(pid);
    },
    { immediate: true },
  );

  // Active ordered id list: the gallery's when walked in from it, else the
  // platform's default alphabetical order. Keyed on membership (not a live
  // platform match) so the list — and the arrows — survive each step, including
  // the brief window where `currentRom` still points at the previous game.
  const index = computed<number[]>(() => {
    if (inGallery.value) return galleryRoms.romIdIndex;
    if (fallbackIndex.value?.ids.includes(toValue(romId))) {
      return fallbackIndex.value.ids;
    }
    return [];
  });

  const position = computed(() => index.value.indexOf(toValue(romId)));

  const hasList = computed(
    () => position.value >= 0 && index.value.length > 1,
  );

  const prevPosition = computed(() =>
    position.value > 0 ? position.value - 1 : null,
  );
  const nextPosition = computed(() =>
    position.value >= 0 && position.value < index.value.length - 1
      ? position.value + 1
      : null,
  );

  function go(target: number | null) {
    if (target === null) return;
    void router.push({
      name: ROUTES.ROM,
      params: { rom: index.value[target] },
      // Keep `?tab=` and friends so stepping doesn't reset to Overview.
      query: route.query,
    });
  }

  const goPrev = () => go(prevPosition.value);
  const goNext = () => go(nextPosition.value);

  return { position, hasList, prevPosition, nextPosition, go, goPrev, goNext };
}
