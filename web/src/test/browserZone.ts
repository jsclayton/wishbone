const resolvedOptions = Intl.DateTimeFormat.prototype.resolvedOptions

/** Sets the timezone that the browser reports, or `undefined` for a browser that can't tell. The test's `vi.restoreAllMocks` undoes it. */
export function browserZone(zone: string | undefined) {
  vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (this: Intl.DateTimeFormat) {
    return { ...resolvedOptions.call(this), timeZone: zone as string }
  })
}
