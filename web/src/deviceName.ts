/**
 * A short, readable name for a User-Agent string. A native app names itself
 * ("Wishbone iOS/1.0"), and a browser is named for its engine and system.
 */
export function deviceName(ua: string): string {
  if (!ua) return 'Unknown device'
  const app = /^Wishbone ([A-Za-z]+)\//.exec(ua)
  if (app) return `Wishbone ${app[1]}`
  const browser =
    /Edg\//.test(ua) ? 'Edge'
    : /Firefox\//.test(ua) ? 'Firefox'
    : /Chrome\/|CriOS\//.test(ua) ? 'Chrome'
    : /Safari\//.test(ua) ? 'Safari'
    : null
  const os =
    /iPhone|iPad/.test(ua) ? 'iOS'
    : /Android/.test(ua) ? 'Android'
    : /Mac OS X/.test(ua) ? 'macOS'
    : /Windows/.test(ua) ? 'Windows'
    : /Linux/.test(ua) ? 'Linux'
    : null
  if (browser && os) return `${browser} on ${os}`
  return browser ?? os ?? 'Unknown device'
}
