import type { Meta, StoryObj } from "@storybook/vue3-vite";
import type { SimpleRom } from "@/stores/roms";
import BrowserCaptureDialog from "./BrowserCaptureDialog.vue";

// Minimal rom stub — the dialog only reads id/name/fs_name.
const rom = {
  id: 1,
  name: "Resident Evil Requiem",
  fs_name: "Resident Evil Requiem.pkg",
} as unknown as SimpleRom;

const pages = [
  { url: "https://example.com/", auto: false },
  { url: "https://1fichier.com/?abcdef123", auto: false },
  { url: "https://vikingfile.com/f/abcdef", auto: true },
];

const meta: Meta<typeof BrowserCaptureDialog> = {
  title: "Dialogs/BrowserCaptureDialog",
  component: BrowserCaptureDialog,
};
export default meta;

type Story = StoryObj<typeof BrowserCaptureDialog>;

export const Default: Story = {
  render: () => ({
    components: { BrowserCaptureDialog },
    setup() {
      return { preview: { rom, pages } };
    },
    template: `<BrowserCaptureDialog :preview="preview" />`,
  }),
};
