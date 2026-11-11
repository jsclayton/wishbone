import { useRef, type ReactNode } from 'react'
import { PHOTO_TYPES } from './api'

/**
 * A button opens the system's file picker. `input` is the picker, hidden, and
 * `open` shows it. The input carries `label` for a screen reader and for a test.
 * Choosing the same file twice still fires: the input is emptied after each
 * choice. The caller puts `input` in its markup and calls `open` from a button.
 */
export function usePhotoPicker(label: string | undefined, onFile: (file: File) => void): { input: ReactNode; open: () => void } {
  const ref = useRef<HTMLInputElement>(null)
  const input = (
    <input
      ref={ref}
      type="file"
      hidden
      accept={PHOTO_TYPES}
      aria-label={label}
      onChange={(e) => {
        const picker = e.currentTarget
        const file = picker.files?.[0]
        picker.value = ''
        if (file) onFile(file)
      }}
    />
  )
  return { input, open: () => ref.current?.click() }
}
