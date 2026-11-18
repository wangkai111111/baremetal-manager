import assert from 'node:assert/strict'
import test from 'node:test'
import { forwardClientIP } from '../dev/clientIp.ts'

for (const [remoteAddress, expected] of [
  ['192.0.2.42', '192.0.2.42'],
  ['::ffff:192.0.2.42', '192.0.2.42'],
  ['2001:db8::42', '2001:db8::42'],
  ['not-an-ip', undefined],
  [undefined, undefined],
] as const) {
  test(`dev ingress derives client IP from socket ${remoteAddress}`, () => {
    const headers = new Map<string, string>([
      ['forwarded', 'for=203.0.113.99'],
      ['x-forwarded-for', '203.0.113.99'],
      ['x-real-ip', '203.0.113.99'],
    ])
    forwardClientIP({
      removeHeader: name => { headers.delete(name.toLowerCase()) },
      setHeader: (name, value) => { headers.set(name.toLowerCase(), value) },
    }, { socket: { remoteAddress } })
    assert.equal(headers.get('x-forwarded-for'), expected)
    assert.equal(headers.has('forwarded'), false)
    assert.equal(headers.has('x-real-ip'), false)
  })
}
