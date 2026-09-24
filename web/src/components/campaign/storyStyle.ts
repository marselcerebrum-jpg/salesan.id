/**
 * The look of a text Story: a colour behind the words and a shape for them.
 *
 * Both travel inside the message WhatsApp delivers, so these are not preview
 * settings. Everyone who opens the status sees what was chosen here.
 *
 * The names are the contract with the server, which translates them into
 * WhatsApp's own enum at send time. Adding one here without adding it there
 * means a Story that is refused on submit, so the two lists are checked against
 * each other by a test on the server side.
 */

export interface StoryFontOption {
  /** What the server stores and translates. */
  id: string;
  /** What the composer calls it. */
  label: string;
  /**
   * What the preview draws it with.
   *
   * An approximation on purpose. These are WhatsApp's own typefaces and are
   * not ours to ship, so the preview reaches for the nearest thing the reader
   * already has. It shows which choice is which, not what the pixels will be.
   */
  css: string;
}

export const STORY_FONTS: StoryFontOption[] = [
  { id: 'system', label: 'Biasa', css: 'system-ui, sans-serif' },
  { id: 'serif', label: 'Mesin Tik', css: '"Courier New", ui-monospace, monospace' },
  { id: 'script', label: 'Sambung', css: '"Brush Script MT", cursive' },
  { id: 'handwriting', label: 'Tulisan Tangan', css: '"Segoe Script", "Bradley Hand", cursive' },
  { id: 'condensed', label: 'Tebal Rapat', css: '"Arial Narrow", "Haettenschweiler", sans-serif' },
  { id: 'heavy', label: 'Tebal Bulat', css: 'Georgia, "Times New Roman", serif' },
];

export interface StoryColourOption {
  label: string;
  /** For the swatch and the preview. */
  hex: string;
  /** What the server sends to WhatsApp: alpha, then the same colour. */
  argb: number;
}

/**
 * WhatsApp's own status palette.
 *
 * Its first entry is the dark teal the server already falls back to, so
 * choosing nothing and choosing the first swatch produce the same Story rather
 * than two that differ by a shade nobody asked about.
 */
export const STORY_COLOURS: StoryColourOption[] = [
  { label: 'Teal', hex: '#075E54', argb: 0xff075e54 },
  { label: 'Hijau', hex: '#1F8A70', argb: 0xff1f8a70 },
  { label: 'Biru', hex: '#1F6FEB', argb: 0xff1f6feb },
  { label: 'Ungu', hex: '#6D3DF5', argb: 0xff6d3df5 },
  { label: 'Merah', hex: '#C2352C', argb: 0xffc2352c },
  { label: 'Jingga', hex: '#D96A1E', argb: 0xffd96a1e },
  { label: 'Kuning', hex: '#C9A227', argb: 0xffc9a227 },
  { label: 'Merah Muda', hex: '#C2407A', argb: 0xffc2407a },
  { label: 'Abu Gelap', hex: '#3B4A54', argb: 0xff3b4a54 },
  { label: 'Hitam', hex: '#111B21', argb: 0xff111b21 },
];

/** The swatch a stored colour belongs to, or the first one when none is set. */
export function colourFor(argb: number | null | undefined): StoryColourOption {
  return STORY_COLOURS.find((c) => c.argb === argb) ?? STORY_COLOURS[0];
}

/** The font a stored id belongs to, or the plain one when none is set. */
export function fontFor(id: string | null | undefined): StoryFontOption {
  return STORY_FONTS.find((f) => f.id === id) ?? STORY_FONTS[0];
}
