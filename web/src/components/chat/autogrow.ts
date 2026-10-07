'use client';

import { useLayoutEffect, type RefObject } from 'react';

/**
 * Grows a textarea to fit what has been typed, up to a ceiling.
 *
 * A fixed one-line box is fine for "ok" and wrong for everything else: a
 * paragraph written into it is read through a slot two words tall, and the
 * sentence being written is the one sentence not on screen. Editing is worse
 * again, because the text is already there — the box opens small over a message
 * that does not fit it and the operator scrolls their own words to find the
 * typo.
 *
 * The ceiling matters as much as the growth. Without one a pasted page pushes
 * the thread off the screen entirely; past it the box stops and scrolls, which
 * is the behaviour of every chat box people already know.
 *
 * `useLayoutEffect` rather than `useEffect`: the height is measured and applied
 * before the browser paints, so the box never flashes at the wrong size between
 * a keystroke and its resize.
 */
export function useAutoGrow(
  ref: RefObject<HTMLTextAreaElement | null>,
  value: string,
  maxRows = 10,
) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;

    // Collapse first. scrollHeight only ever reports the content height when
    // the element is not already tall enough to hold it, so measuring without
    // this makes a box that grows and never shrinks back.
    el.style.height = 'auto';

    const style = window.getComputedStyle(el);
    const lineHeight = parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.5 || 20;
    const padding = parseFloat(style.paddingTop) + parseFloat(style.paddingBottom);
    const border = parseFloat(style.borderTopWidth) + parseFloat(style.borderBottomWidth);

    const ceiling = lineHeight * maxRows + padding + border;
    const wanted = el.scrollHeight + border;

    el.style.height = `${Math.min(wanted, ceiling)}px`;
    el.style.overflowY = wanted > ceiling ? 'auto' : 'hidden';
  }, [ref, value, maxRows]);
}
