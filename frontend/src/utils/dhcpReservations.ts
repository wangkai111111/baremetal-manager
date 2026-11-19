export type DHCPReservation = {
  name: string; enabled: boolean; circuit_id: string; circuit_format: 'text' | 'hex';
  remote_id: string; remote_format: 'text' | 'hex'; mac: string; ip: string
}
export const newDHCPReservation = (): DHCPReservation => ({name: '', enabled: true, circuit_id: '', circuit_format: 'text', remote_id: '', remote_format: 'text', mac: '', ip: ''})

function ipv4(value: string): number | undefined {
  const parts = value.split('.')
  if (parts.length !== 4 || parts.some(p => !/^(0|[1-9]\d{0,2})$/.test(p) || Number(p) > 255)) return
  return parts.reduce((sum, part) => sum * 256 + Number(part), 0)
}
function relayBytes(value: string, format: string): string {
  if (format !== 'text' && format !== 'hex') throw Error('标识格式只能选择文本或十六进制')
  if (!value) return ''
  let bytes: number[]
  if (format === 'hex') {
    if (!/^[a-f\d]{2}(?::[a-f\d]{2})*$/i.test(value)) throw Error('十六进制标识需按字节用冒号分隔，例如 00:04:00:64')
    bytes = value.split(':').map(p => parseInt(p, 16))
  } else {
    if (!value.trim() || /[\p{Cc}]/u.test(value)) throw Error('文本标识不能为空白或包含控制字符')
    bytes = [...new TextEncoder().encode(value)]
  }
  if (bytes.length > 128) throw Error('每个 Relay 标识最多 128 字节')
  return bytes.map(v => v.toString(16).padStart(2, '0')).join(':')
}
export function validateDHCPReservations(rules: DHCPReservation[], context: {subnet: string; gateway: string; server: string; mode: string; taggedOnly: boolean; relayProxy: boolean}): string {
  if (rules.length > 128) return '标签与固定 IP 规则最多 128 条'
  if (context.relayProxy && context.mode !== 'relay') return '续租 Relay 代理只能在三层 DHCP Relay 模式启用'
  const active: {circuit: string; remote: string; mac: string; ip: string; name: string}[] = []
  const [network, bitsText] = context.subnet.split('/'), bits = Number(bitsText), base = ipv4(network || '')
  const size = 2 ** (32 - bits)
  for (const [i, rule] of rules.entries()) {
    const fail = (message: string) => `第 ${i + 1} 条规则【${rule.name || '未命名'}】：${message}`
    if (!rule.name.trim() || [...rule.name].length > 80 || /[\p{Cc}]/u.test(rule.name)) return fail('规则名称必填，最多 80 个字符且不含控制字符')
    let circuit: string, remote: string
    try { circuit = relayBytes(rule.circuit_id, rule.circuit_format || 'text'); remote = relayBytes(rule.remote_id, rule.remote_format || 'text') }
    catch (e) { return fail((e as Error).message) }
    if ((circuit ? circuit.split(':').length : 0) + (remote ? remote.split(':').length : 0) > 251) return fail('两个 Relay 标识合计不能超过 251 字节')
    const mac = rule.mac.toLowerCase().replaceAll('-', ':')
    if (mac && (!/^([a-f\d]{2}:){5}[a-f\d]{2}$/.test(mac) || (parseInt(mac.slice(0, 2), 16) & 1) || mac === '00:00:00:00:00:00')) return fail('MAC 必须是完整、非零的单播 MAC 地址，不支持通配符')
    if (!circuit && !remote && !mac) return fail('至少填写 Circuit-ID、Remote-ID 或 MAC 中的一项')
    if ((circuit || remote) && context.mode !== 'relay') return fail('Option 82 标签规则需要三层 DHCP Relay 模式')
    const ip = ipv4(rule.ip)
    if (base === undefined || !Number.isInteger(bits) || bits < 1 || bits > 30 || ip === undefined || ip <= base || ip >= base + size - 1 || rule.ip === context.gateway || rule.ip === context.server) return fail('固定 IP 必须属于装机子网，且不能使用网络、广播、网关或 PXE 服务器地址')
    if (!rule.enabled) continue
    for (const old of active) {
      if (old.ip === rule.ip) return fail(`固定 IP 已由规则【${old.name}】使用`)
      if ((!circuit || !old.circuit || circuit === old.circuit) && (!remote || !old.remote || remote === old.remote) && (!mac || !old.mac || mac === old.mac)) return fail(`匹配条件与规则【${old.name}】可能同时命中，请用 Remote-ID / Circuit-ID / MAC 区分设备`)
    }
    active.push({circuit, remote, mac, ip: rule.ip, name: rule.name})
  }
  return context.taggedOnly && !active.length ? '仅按规则分配时，至少需要一条已启用的规则' : ''
}
