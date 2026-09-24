/**
 * The look of a text Story: a colour behind the words and a shape for them.
 *
 * Both travel inside the message WhatsApp delivers, so these are not preview
 * settings. Everyone who opens the status sees what was chosen here.
 *
 * The ids are the contract with the server, which translates them into
 * WhatsApp's own enum at send time. Adding one here without adding it there
 * means a Story that is refused on submit, so the two lists are checked against
 * each other by a test on the server side.
 */

import { Calistoga, Caveat, Courier_Prime, Exo_2, Norican } from 'next/font/google';

/*
 * Three of WhatsApp's faces are published under an open licence, so the preview
 * can draw the real thing rather than guess at it. They are fetched when the
 * site is built and served from our own domain, which keeps the reader's
 * browser from announcing itself to a font host on every visit.
 *
 * The other two are not ours to ship. What stands in for them is chosen to be
 * the same kind of letter and, more importantly, to exist everywhere: the
 * previous stand-ins were fonts installed on Windows, which meant a phone
 * browser drew two different choices with the same default letters and the
 * picker looked broken.
 */
const typewriter = Courier_Prime({ subsets: ['latin'], weight: '700' });
const condensed = Exo_2({ subsets: ['latin'], weight: '800' });
const rounded = Calistoga({ subsets: ['latin'], weight: '400' });
const script = Norican({ subsets: ['latin'], weight: '400' });
const handwriting = Caveat({ subsets: ['latin'], weight: '700' });

export interface StoryFontOption {
  /** What the server stores and translates. Frozen: old campaigns carry it. */
  id: string;
  /** What the composer calls it. */
  label: string;
  /** What the preview draws it with. */
  css: string;
  /** Only where the face needs one; the plain weight is left alone. */
  weight?: number;
  /**
   * Whether the preview shows the letters that will actually be sent.
   *
   * Recorded per font rather than stated once for all of them, because it is
   * true of six of the eight, and telling someone their preview is
   * approximate when it is exact is its own kind of wrong.
   */
  exact: boolean;
}

/**
 * Every font WhatsApp has, in the order the composer shows them.
 *
 * Eight, and not a shortlist of eight: WhatsApp's protocol defines exactly this
 * many. The first three are the phone's own letters in three weights, so they
 * are exact by definition, whatever font the phone happens to use.
 */
export const STORY_FONTS: StoryFontOption[] = [
  { id: 'system', label: 'Biasa', css: 'system-ui, sans-serif', exact: true },
  { id: 'system-text', label: 'Polos', css: 'system-ui, sans-serif', weight: 400, exact: true },
  { id: 'system-bold', label: 'Tebal', css: 'system-ui, sans-serif', weight: 700, exact: true },
  { id: 'serif', label: 'Mesin Tik', css: typewriter.style.fontFamily, weight: 700, exact: true },
  { id: 'condensed', label: 'Tebal Rapat', css: condensed.style.fontFamily, weight: 800, exact: true },
  { id: 'heavy', label: 'Tebal Bulat', css: rounded.style.fontFamily, exact: true },
  { id: 'script', label: 'Sambung', css: script.style.fontFamily, exact: false },
  { id: 'handwriting', label: 'Tulisan Tangan', css: handwriting.style.fontFamily, weight: 700, exact: false },
];

/** The fonts whose preview is a stand-in, for the note under the picker. */
export const APPROXIMATE_FONTS = STORY_FONTS.filter((f) => !f.exact);

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
