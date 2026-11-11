/**
 * The look of a button that does nothing: `mute` text and a 2px dashed `mute`
 * edge. The edge is an outline pulled inside the box, so the button keeps its
 * size. `aria-disabled` is for a button that must keep focus when it turns off.
 * That outline would hide the browser's focus ring, so a focused one shows a
 * solid `ink` ring outside its box instead.
 */
const off =
  'disabled:text-mute disabled:outline-2 disabled:-outline-offset-2 disabled:outline-dashed disabled:outline-mute aria-disabled:text-mute aria-disabled:outline-2 aria-disabled:-outline-offset-2 aria-disabled:outline-dashed aria-disabled:outline-mute aria-disabled:focus-visible:outline-solid aria-disabled:focus-visible:outline-ink aria-disabled:focus-visible:outline-offset-2'

/** A secondary action: 56px high, 18px text at weight 800, on `tile`. Disabled, it is see-through with a dashed edge. Heights are minimums, so long labels wrap. */
export const button =
  `min-h-14 rounded-md bg-tile px-6 py-3 text-lg font-extrabold text-ink disabled:bg-transparent aria-disabled:bg-transparent ${off}`

/** A link that looks like `button`, for a button that opens a screen. */
export const buttonLink = `flex items-center justify-center ${button}`

/** The main action of a screen: 62px high, 20px text at weight 800, on `btn`. Disabled, it is on `tile` with a dashed edge. Use it once per screen. */
export const primary =
  `min-h-15.5 rounded-md bg-btn px-6 py-3 text-xl font-extrabold text-on-btn disabled:bg-tile aria-disabled:bg-tile ${off}`

export const field =
  'min-h-14.5 w-full rounded-md border-2 border-field bg-transparent px-3 py-2 text-lg placeholder:text-mute'

/** A dropdown. Safari ignores min-height on a native select, so it needs a fixed height. */
export const select = `${field} h-14.5`

/** A link on its own line. Links inside a sentence are only underlined. */
export const link = 'flex min-h-12 items-center underline'

/**
 * A note link that is the whole of its line, as in a list of shops. Unlike
 * `link`, it is inline-flex: `flex` would add a blank line next to the `<br>`
 * between paragraph lines. It has a minimum width too, because a short link
 * (one letter) is narrower than a finger. Padding would not do: padded links
 * in a list can overlap.
 */
export const aloneLink = 'inline-flex min-h-12 min-w-11 items-center underline'

/** A file picker. Its native button is styled as a secondary button (same size and weight as `button`), tall enough to tap on a phone. */
export const filePicker =
  'min-h-14 w-full file:mr-3 file:min-h-14 file:rounded-md file:border-0 file:bg-tile file:px-6 file:text-lg file:font-extrabold file:text-ink'
