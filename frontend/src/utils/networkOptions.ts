// PostgreSQL inet values include a host mask; form controls accept host addresses.
export function hostIP(value: unknown): string { return value == null ? '' : String(value).split('/')[0]! }
export function dnsAddresses(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(hostIP)
  return String(value || '').replace(/[{}"\[\]]/g, '').split(/[,\s]+/).filter(Boolean).map(hostIP)
}
export function networkLabel(row: Record<string, unknown>): string { return `${row.name} · ${row.subnet}${row.vlan_id ? ` · VLAN ${row.vlan_id}` : ''}` }
