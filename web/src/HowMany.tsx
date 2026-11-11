import { button } from './ui'

/**
 * The One fewer and One more buttons around a count, shared by "I'll get this"
 * and the gifts in Giving. With no `most`, there is no top. `label` says which
 * item the buttons are for, where one screen has several.
 */
export function HowMany({ value, most, label, onChange }: { value: number; most?: number; label?: string; onChange: (n: number) => void }) {
  const suffix = label ? `: ${label}` : ''
  return (
    // aria-disabled, not disabled, so a pressed button keeps focus.
    <div className="grid grid-cols-[4rem_minmax(0,1fr)_4rem] items-center gap-2">
      <button type="button" className={`${button} text-[1.625rem]`} aria-label={`One fewer${suffix}`} aria-disabled={value <= 1} onClick={() => value > 1 && onChange(value - 1)}>
        -
      </button>
      <span className="text-center font-mono text-[1.625rem] font-semibold">{value}</span>
      <button
        type="button"
        className={`${button} text-[1.625rem]`}
        aria-label={`One more${suffix}`}
        aria-disabled={most !== undefined && value >= most}
        onClick={() => (most === undefined || value < most) && onChange(value + 1)}
      >
        +
      </button>
    </div>
  )
}
