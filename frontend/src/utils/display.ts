import { hostIP } from './networkOptions.ts'

// Database keys belong in requests and row keys, never in identity labels.
export function assetLabel(row: Record<string, unknown> | null | undefined): string {
  if (!row) return '服务器信息不可用'
  const parts = [String(row.name || row.asset_name || '').trim()]
  const addresses = [...new Set([hostIP(row.management_ip), hostIP(row.bmc_address)].filter(Boolean))]
  parts.push(...addresses)
  if (row.serial_number) parts.push(`SN: ${row.serial_number}`)
  return parts.filter(Boolean).join(' · ') || '服务器信息不可用'
}

export function personLabel(name: unknown, username?: unknown): string {
  return [...new Set([name, username].map(v => String(v || '').trim()).filter(Boolean))].join(' · ') || '操作人信息不可用'
}
