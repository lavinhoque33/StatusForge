/**
 * The interval a form should send, tracked per form instance.
 *
 * `IntervalField` reports the user's selection here; the pages read it on
 * submit. A `null` choice means "no explicit choice": create sends nothing (the
 * backend default applies) and PATCH omits `intervalSeconds` (the stored value
 * is kept), matching the contract's optional handling on both routes.
 */
export type IntervalChoice = { current: number | null };

export function intervalChoice(): IntervalChoice {
  return { current: null };
}
