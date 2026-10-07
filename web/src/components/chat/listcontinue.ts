'use client';

/** A line break that carries the list marker the line above it was using. */
export interface ListBreak {
  text: string;
  caret: number;
}

/**
 * The markers a line can be wearing.
 *
 * Bullets and numbers both, because an operator writing a price list reaches
 * for whichever one WhatsApp renders — and WhatsApp renders neither specially,
 * which is exactly why this matters: the list is held together by the
 * characters typed, so a line that forgets its dash is a line that falls out of
 * the list for the reader.
 */
const MARKER = /^(\s*)(?:([-*•])|(\d+)([.)]))(\s+)(.*)$/;

/**
 * Continues a list when the operator breaks the line inside one.
 *
 * Typing a seven-item price list means typing "- " seven times, and the seventh
 * is the one that gets forgotten. Every editor people already use — Notes,
 * Docs, every chat app with formatting — carries the marker down for them, so
 * its absence here reads as the field being broken rather than plain.
 *
 * Returns null when there is nothing to continue, and the caller inserts an
 * ordinary newline.
 *
 * Breaking the line on an *empty* item ends the list instead of adding another
 * empty one. That is how every one of those editors behaves, and it is the only
 * way out of a list that does not involve deleting the marker by hand.
 */
export function continueList(value: string, caret: number): ListBreak | null {
  const lineStart = value.lastIndexOf('\n', caret - 1) + 1;
  const line = value.slice(lineStart, caret);

  const m = MARKER.exec(line);
  if (!m) return null;

  const [, indent, bullet, number, numberPunct, gap, content] = m;

  // An empty item: the operator is leaving the list, not extending it.
  if (content.trim() === '') {
    const text = value.slice(0, lineStart) + value.slice(caret);
    return { text, caret: lineStart };
  }

  const next = bullet
    ? `${indent}${bullet}${gap}`
    : `${indent}${Number(number) + 1}${numberPunct}${gap}`;

  return {
    text: `${value.slice(0, caret)}\n${next}${value.slice(caret)}`,
    caret: caret + 1 + next.length,
  };
}

/**
 * Applies a continued line break to a controlled textarea.
 *
 * The caret has to be restored after React has rendered the new value, or it
 * lands at the end of the box and the next character is typed in the wrong
 * place. Hence the frame of delay — there is no synchronous moment between the
 * state update and the DOM carrying it.
 */
export function applyListBreak(
  el: HTMLTextAreaElement,
  value: string,
  setValue: (next: string) => void,
): boolean {
  const broken = continueList(value, el.selectionStart ?? value.length);
  if (!broken) return false;

  setValue(broken.text);
  requestAnimationFrame(() => {
    el.selectionStart = broken.caret;
    el.selectionEnd = broken.caret;
  });
  return true;
}
