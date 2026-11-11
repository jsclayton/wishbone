import { deviceName } from './deviceName'

test.each([
  ['', 'Unknown device'],
  ['Wishbone iOS/1.0', 'Wishbone iOS'],
  ['Wishbone Android/2.3 (Pixel)', 'Wishbone Android'],
  [
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36',
    'Chrome on macOS',
  ],
  ['Mozilla/5.0 (X11; Linux x86_64; rv:126.0) Gecko/20100101 Firefox/126.0', 'Firefox on Linux'],
  ['curl/8.0', 'Unknown device'],
])('%s is named %s', (ua, name) => {
  expect(deviceName(ua)).toBe(name)
})
