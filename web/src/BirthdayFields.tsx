import type { PartialDate } from './api'
import { field, select } from './ui'

export const months = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
]

/** What a person typed into the birthday inputs. A form holds it as text. */
export type BirthdayText = { month: string; day: string; year: string }

export const emptyBirthday: BirthdayText = { month: '', day: '', year: '' }

/** The birthday as the API takes it, or null while the month or the day is empty. */
export function toPartialDate({ month, day, year }: BirthdayText): PartialDate | null {
  if (!month || !day) return null
  return { month: Number(month), day: Number(day), year: year ? Number(year) : null }
}

const number = '[appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none'

/** The birthday inputs. The month and day are required, and the year is optional. */
export function BirthdayFields({ value, onChange }: { value: BirthdayText; onChange: (v: BirthdayText) => void }) {
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="pb-1.5 font-semibold">Birthday</legend>
      <div className="grid grid-cols-2 gap-2 min-[360px]:grid-cols-[minmax(0,1fr)_5rem_6rem]">
        <label className="col-span-2 flex flex-col gap-1 min-[360px]:col-span-1">
          <span className="text-base">Month</span>
          <select className={`${select} px-2!`} value={value.month} onChange={(e) => onChange({ ...value, month: e.target.value })}>
            <option value="">Choose</option>
            {months.map((m, i) => (
              <option key={m} value={i + 1}>
                {m}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-base">Day</span>
          <input
            className={`${field} px-2! ${number}`}
            type="number"
            inputMode="numeric"
            min={1}
            max={31}
            value={value.day}
            onChange={(e) => onChange({ ...value, day: e.target.value })}
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-base">Year</span>
          <input
            className={`${field} px-2! ${number}`}
            type="number"
            inputMode="numeric"
            placeholder="Optional"
            value={value.year}
            onChange={(e) => onChange({ ...value, year: e.target.value })}
          />
        </label>
      </div>
    </fieldset>
  )
}
