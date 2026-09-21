/**
 * One shape for every control the user can click into or type into.
 *
 * Before this there were five copy-pasted definitions of "an input" and six
 * different heights in use: 30, 34, 35, 36, 40 and 44 pixels. Two of the five
 * copies were byte-identical, two disagreed about the font size (12px in one
 * screen, 15px in the next), and one used vertical padding instead of a height
 * so it did not line up with anything. A button beside an input was 44px tall
 * next to 34px.
 *
 * That is what "doesn't fit" looks like from the outside: nothing is wrong on
 * its own, but nothing lines up, and every screen looks slightly assembled by
 * a different person.
 *
 * Two heights, and they are the same two a button gets, so a filter row, a
 * search field and the button beside them share one baseline:
 *
 *   sm  36px  toolbars, filter rows, anywhere controls sit in a dense line
 *   md  40px  forms and dialogs, where a control is the thing being used
 *
 * Both carry the body step (15px). A control the user types into should not be
 * smaller than the text around it, which is what the 12px copies were doing.
 */

/** Shape, border and focus. Everything a control shares regardless of size. */
const base =
  'rounded-control border border-hairline bg-surface-raised text-sm text-ink ' +
  'outline-none transition-colors placeholder:text-ink-muted ' +
  'focus:border-brand-600 focus-visible:outline-2 focus-visible:outline-offset-1 ' +
  'focus-visible:outline-brand-600 disabled:cursor-not-allowed disabled:opacity-60';

/** 36px. For a control standing in a row of other controls. */
export const controlSm = `h-9 px-2.5 ${base}`;

/** 40px. For a control that is the subject of the form it sits in. */
export const controlMd = `h-10 px-3 ${base}`;

/** The default: a full-width field in a form or dialog. */
export const inputClass = `w-full ${controlMd}`;

/** The compact default: a field in a filter bar or toolbar. */
export const inputClassSm = controlSm;

/**
 * Multi-line input. No height, since it grows, but the same everything else so
 * a textarea under an input does not look like it came from another app.
 */
export const textareaClass = `w-full py-2 px-3 ${base}`;

/** The label above a field. */
export const labelClass = 'mb-1.5 block text-xs font-medium text-ink-soft';

/**
 * Button heights, exported so the two button implementations in this codebase
 * cannot drift apart from the fields they stand next to.
 */
export const BUTTON_HEIGHT = { sm: 'h-9', md: 'h-10' } as const;
