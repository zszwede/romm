// useRomHotkeys: keyboard shortcuts active on the game detail page.
//
//   ArrowLeft  / ArrowRight → previous / next game in the current gallery order
//   m                       → open the Match ROM dialog
//   d                       → (virtual game) flag it for download
//   Delete                  → (virtual game) delete AND exclude it
//
// Mounted from GameDetails.vue, so the listener only exists while a game page
// is open and is torn down on leave. Guarded like useGlobalHotkeys: never fires
// in a text field / contenteditable, with a modifier held, or while a game is
// running (the emulator owns the keyboard then).
import { useEventListener } from "@vueuse/core";
import type { SimpleRom } from "@/stores/roms";
import storePlaying from "@/stores/playing";
import { hasOpenEscapable } from "@/v2/lib/overlays/RDialog/escapeStack";
import { useGalleryNeighbors } from "@/v2/composables/useGalleryNeighbors";
import { useGameActions } from "@/v2/composables/useGameActions";
import { isEditable } from "@/v2/utils/editable";

export function useRomHotkeys(getRom: () => SimpleRom | null | undefined) {
  const playingStore = storePlaying();
  const neighbors = useGalleryNeighbors(() => getRom()?.id ?? -1);
  const actions = useGameActions(getRom);

  function onKey(e: KeyboardEvent) {
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    if (isEditable(e.target)) return;
    if (playingStore.playing) return;
    // Don't steal keys while an overlay owns the keyboard: a dialog/menu on the
    // escape stack, or the fullscreen screenshot carousel (which runs its own
    // window-level Arrow nav between images). Without this, ArrowLeft/Right
    // would step to the next game instead of the next screenshot.
    if (hasOpenEscapable()) return;
    if (
      typeof document !== "undefined" &&
      document.querySelector(".r-carousel--fullscreen")
    ) {
      return;
    }

    const rom = getRom();
    if (!rom) return;

    switch (e.key) {
      case "ArrowLeft":
        neighbors.goPrev();
        e.preventDefault();
        break;
      case "ArrowRight":
        neighbors.goNext();
        e.preventDefault();
        break;
      case "m":
      case "M":
        if (actions.canMatch.value) {
          actions.match();
          e.preventDefault();
        }
        break;
      case "d":
      case "D":
        if (rom.is_virtual) {
          void actions.markDownload();
          e.preventDefault();
        }
        break;
      case "Delete":
        if (actions.canExclude.value) {
          void actions.excludeRom();
          e.preventDefault();
        }
        break;
    }
  }

  useEventListener(window, "keydown", onKey);
}
