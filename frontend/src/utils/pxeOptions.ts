export type HostAddress = { address: string; prefix: string; subnet: string }
export type HostInterface = { name: string; mac: string; up: boolean; addresses: HostAddress[] }
export type BootFile = { name: string; label: string }
export type BootRoot = { path: string; bios_files: BootFile[]; efi_files: BootFile[]; chain_ready: boolean }
export type HostOptions = { interfaces: HostInterface[]; roots: BootRoot[] }
export type SelectOption = { label: string; value: string; disabled?: boolean }

function ipv4(value: string): number | undefined {
  const parts = value.split('.')
  if (parts.length !== 4 || parts.some(part => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255)) return
  return parts.reduce((result, part) => result * 256 + Number(part), 0)
}
export function hostInterfaceOptions(interfaces: HostInterface[]): SelectOption[] {
  return interfaces.map(row => ({ value: row.name,
    label: `${row.name} · ${row.addresses.map(ip => ip.prefix).join(' / ') || '无 IPv4'}${row.up ? '' : ' · 未启用'}`,
    disabled: !row.up || !row.addresses.length }))
}
export function hostAddressOptions(iface: HostInterface | undefined, subnet = '', start = '', end = '', mode = 'direct'): SelectOption[] {
  return (iface?.addresses || []).map(row => {
    let reason = !iface?.up ? '网卡未启用' : ''
    if (!reason && mode !== 'relay' && subnet && row.subnet !== subnet) reason = '与装机子网不匹配'
    const ip = ipv4(row.address), first = ipv4(start), last = ipv4(end)
    if (!reason && ip !== undefined && first !== undefined && last !== undefined && ip >= first && ip <= last) reason = '位于 DHCP 地址池内'
    return {value: row.address, label: `${row.prefix}${reason ? `（${reason}）` : ''}`, disabled: !!reason}
  })
}
export function retainOrChooseAddress(options: SelectOption[], current: string): string {
  const available = options.filter(row => !row.disabled)
  if (available.some(row => row.value === current)) return current
  return available.length === 1 ? available[0]!.value : ''
}
export function includeSavedOption(options: SelectOption[], value: string): SelectOption[] {
  return value && !options.some(row => row.value === value)
    ? [...options, {value, label: `${value}（当前配置，系统未检测到）`, disabled: true}] : options
}
