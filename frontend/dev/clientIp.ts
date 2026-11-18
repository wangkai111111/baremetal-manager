import { isIP } from 'node:net'

type ProxyRequest = {
  removeHeader(name: string): void
  setHeader(name: string, value: string): void
}

// The public dev server is an ingress: overwrite client-supplied forwarding
// headers rather than appending an address that a caller could spoof.
export function forwardClientIP(proxyRequest: ProxyRequest, request: { socket: { remoteAddress?: string } }) {
  for (const header of ['forwarded', 'x-forwarded-for', 'x-real-ip']) proxyRequest.removeHeader(header)
  const remote = request.socket.remoteAddress || ''
  const mapped = remote.startsWith('::ffff:') ? remote.slice(7) : ''
  const clientIP = isIP(mapped) === 4 ? mapped : remote
  if (isIP(clientIP)) proxyRequest.setHeader('X-Forwarded-For', clientIP)
}
