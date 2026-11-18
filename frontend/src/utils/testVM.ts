export function isTestVM(row: Record<string, unknown>): boolean {
  return row.manufacturer === 'BMM Test VM' && row.model === 'QEMU PXE Lab' && String(row.mac_address).toLowerCase() === '52:54:00:77:00:10'
}
