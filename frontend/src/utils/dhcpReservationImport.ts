import { newDHCPReservation, validateDHCPReservations, type DHCPReservation } from './dhcpReservations.ts'

export type ReservationContext = Parameters<typeof validateDHCPReservations>[1]
export type ImportMode = 'append' | 'replace'
export type ImportRow = { line: number; rule: DHCPReservation; error: string }
export type ImportTable = { rows: ImportRow[]; error: string }
export type ImportSheet = { name: string; table: ImportTable }
export type TemplateKind = 'option82' | 'mac'
const MAX_ROWS = 128
const MAX_TEXT = 200_000
const MAX_SHEET_ROWS = 1000
const headers = ['规则名称', '固定IP', 'Circuit-ID', 'Circuit-ID格式', 'Remote-ID', 'Remote-ID格式', 'MAC', '启用']
const normalizeHeader = (value: string) => value.trim().toLowerCase().replace(/[\s_\-（）()]/g, '')
const aliases: Record<string, keyof DHCPReservation> = Object.create(null)
for (const [key, names] of Object.entries({
  name: ['规则名称', '名称', 'name'], ip: ['固定IP', '固定IPv4地址', '固定IP地址', 'IP地址', 'IP', 'IPv4'],
  circuit_id: ['Circuit-ID', 'Circuit-ID（端口标识）', '端口标识', 'circuit_id'], circuit_format: ['Circuit-ID格式', 'Circuit格式', 'circuit_format'],
  remote_id: ['Remote-ID', 'Remote-ID（交换机标识）', '交换机标识', 'remote_id'], remote_format: ['Remote-ID格式', 'Remote格式', 'remote_format'],
  mac: ['MAC', 'MAC地址', '服务器MAC'], enabled: ['启用', '是否启用', '状态', 'enabled'],
})) for (const name of names) aliases[normalizeHeader(name)] = key as keyof DHCPReservation

function format(value: string): 'text' | 'hex' {
  const text = value.trim().toLowerCase()
  if (!text || ['text', '文本'].includes(text)) return 'text'
  if (['hex', '十六进制', '16进制'].includes(text)) return 'hex'
  throw Error('标识格式只接受 文本 / 十六进制（或 text / hex）')
}
function enabled(value: string): boolean {
  const text = value.trim().toLowerCase()
  if (!text || ['1', 'true', '是', '启用', '已启用', '开启'].includes(text)) return true
  if (['0', 'false', '否', '停用', '已停用', '禁用', '关闭'].includes(text)) return false
  throw Error('启用列只接受 是 / 否、启用 / 停用、true / false 或 1 / 0')
}

// Keep Relay IDs byte-exact: only names, IPs, MACs and enum values are trimmed.
export function parseReservationTable(matrix: unknown[][], lines?: number[]): ImportTable {
  const nonempty = matrix.map((cells, index) => ({ cells: cells.map(v => v == null ? '' : String(v)), line: lines?.[index] ?? index + 1 }))
    .filter(row => row.cells.some(v => v.trim()))
  const fail = (error: string): ImportTable => ({ rows: [], error })
  const header = nonempty.shift()
  if (!header) return fail('没有可导入的内容，请保留表头并填写规则。')
  if (header.cells.length > 32) return fail('列数超过限制，请使用提供的模板（最多 32 列）。')
  const columns = header.cells.map(value => value.trim() ? aliases[normalizeHeader(value.replace(/^\uFEFF/, ''))] : undefined)
  const seen = new Set<string>()
  for (const [index, key] of columns.entries()) {
    if (!key && header.cells[index]?.trim()) return fail(`第 ${header.line} 行：无法识别列“${header.cells[index]}”，请使用模板表头。`)
    if (key && seen.has(key)) return fail(`第 ${header.line} 行：列“${header.cells[index]}”重复。`)
    if (key) seen.add(key)
  }
  if (!seen.has('name') || !seen.has('ip')) return fail('表头必须包含“规则名称”和“固定IP”。')
  if (!['circuit_id', 'remote_id', 'mac'].some(key => seen.has(key))) return fail('表头至少需要 Circuit-ID、Remote-ID 或 MAC 中的一列。')
  if (!nonempty.length) return fail('只有表头，没有可导入的规则。')
  if (nonempty.length > MAX_ROWS) return fail(`单次最多导入 ${MAX_ROWS} 条规则，当前有 ${nonempty.length} 条。`)
  return { error: '', rows: nonempty.map(({ cells, line }) => {
    const rule = newDHCPReservation()
    const values = new Map(columns.flatMap((key, index) => key ? [[key, cells[index] || ''] as const] : []))
    let error = ''
    try {
      if (cells.some((value, index) => value.trim() && !columns[index])) throw Error('存在没有表头的非空列，请核对分隔符和表头。')
      rule.name = (values.get('name') || '').trim(); rule.ip = (values.get('ip') || '').trim()
      rule.mac = (values.get('mac') || '').trim().replaceAll('-', ':').toLowerCase()
      rule.circuit_id = values.get('circuit_id') || ''; rule.remote_id = values.get('remote_id') || ''
      rule.circuit_format = format(values.get('circuit_format') || ''); rule.remote_format = format(values.get('remote_format') || '')
      rule.enabled = enabled(values.get('enabled') || '')
    } catch (e) { error = (e as Error).message }
    return { line, rule, error }
  }) }
}

// Parse TSV copied from Excel or RFC-style CSV, including quoted commas/quotes.
export function parseReservationText(input: string): ImportTable {
  if (input.length > MAX_TEXT) return { rows: [], error: '粘贴内容过大，请控制在 20 万字符以内。' }
  const text = input.replace(/^\uFEFF/, '').replace(/\r\n?/g, '\n')
  let quoted = false, delimiter = ',', firstRecord = ''
  for (let i = 0; i < text.length; i++) {
    const char = text[i]!
    if (char === '"') { if (quoted && text[i + 1] === '"') { i++; continue }; quoted = !quoted }
    if (!quoted && char === '\n') {
      if (firstRecord.trim()) break
      firstRecord = ''; delimiter = ','; continue
    }
    if (!quoted && char === '\t') delimiter = '\t'
    firstRecord += char
  }
  if (!firstRecord.trim()) return { rows: [], error: '请从模板或 Excel 复制包含表头的内容。' }
  const matrix: string[][] = [], lines: number[] = []
  let cells: string[] = [], cell = '', inQuote = false, closedQuote = false, line = 1, rowLine = 1
  const fail = (error: string): ImportTable => ({ rows: [], error: `第 ${line} 行：${error}` })
  for (let i = 0; i <= text.length; i++) {
    const char = text[i]
    if (inQuote) {
      if (char === undefined) return fail('双引号未闭合。')
      if (char === '"') { if (text[i + 1] === '"') { cell += '"'; i++ } else { inQuote = false; closedQuote = true } }
      else { cell += char; if (char === '\n') line++ }
      continue
    }
    if (char === delimiter || char === '\n' || char === undefined) {
      cells.push(cell); cell = ''; closedQuote = false
      if (cells.length > 32) return fail('列数超过限制，请使用模板。')
      if (char !== delimiter) {
        matrix.push(cells); lines.push(rowLine); cells = []; line++; rowLine = line
        if (matrix.length > MAX_SHEET_ROWS) return fail('表格超过 1000 行，请删除多余空行，单次最多导入 128 条规则。')
      }
    } else if (char === '"' && !cell && !closedQuote) inQuote = true
    else {
      if (closedQuote || char === '"') return fail('双引号或分隔符格式不正确；字段内的引号需写成两个双引号。')
      cell += char
    }
  }
  return parseReservationTable(matrix, lines)
}

export function previewReservationImport(table: ImportTable, existing: DHCPReservation[], mode: ImportMode, context: ReservationContext) {
  const accepted = mode === 'append' ? existing.map(row => ({...row})) : []
  let error = table.error
  if (!error && accepted.length + table.rows.length > MAX_ROWS) error = `导入后将有 ${accepted.length + table.rows.length} 条规则，超过 128 条；请减少规则或选择替换全部。`
  if (!error) error = validateDHCPReservations(accepted, {...context, taggedOnly: false})
  const rows = table.rows.map(row => {
    const validation = row.error || validateDHCPReservations([...accepted, row.rule], {...context, taggedOnly: false})
    if (!validation) accepted.push({...row.rule})
    return {...row, error: validation.replace(/^第 \d+ 条规则【.*?】：/, '')}
  })
  if (!error && !rows.some(row => row.error)) error = validateDHCPReservations(accepted, context)
  return { rows, error, rules: accepted, valid: !!rows.length && !error && rows.every(row => !row.error) }
}

export async function readReservationWorkbook(data: ArrayBuffer): Promise<ImportSheet[]> {
  const XLSX = await import('xlsx')
  const workbook = XLSX.read(data, {type: 'array', sheetRows: MAX_SHEET_ROWS + 1, cellFormula: false, cellHTML: false, cellStyles: false, cellText: false})
  if (!workbook.SheetNames.length) throw Error('Excel 文件没有工作表。')
  if (workbook.SheetNames.length > 16) throw Error('Excel 最多支持 16 个工作表，请使用模板。')
  return workbook.SheetNames.map(name => {
    const sheet = workbook.Sheets[name]!
    const range = XLSX.utils.decode_range(sheet['!fullref'] || sheet['!ref'] || 'A1')
    if (range.e.r >= MAX_SHEET_ROWS || range.e.c >= 32) return { name, table: {rows: [], error: '工作表超过 1000 行或 32 列，请删除多余区域后重试。'} }
    return { name, table: parseReservationTable(XLSX.utils.sheet_to_json<unknown[]>(sheet, {header: 1, raw: true, defval: '', blankrows: true, range: 0})) }
  })
}

function exampleIPs(context: Pick<ReservationContext, 'subnet' | 'gateway' | 'server'>): string[] {
  const [network, prefix] = context.subnet.split('/')
  const parts = (network || '').split('.').map(Number), bits = Number(prefix)
  if (parts.length !== 4 || parts.some(v => !Number.isInteger(v) || v < 0 || v > 255) || !Number.isInteger(bits) || bits < 1 || bits > 30) return ['10.30.0.110', '10.30.0.111']
  const base = parts.reduce((sum, value) => sum * 256 + value, 0), size = 2 ** (32 - bits)
  const ips: string[] = []
  for (const offset of [...(size > 112 ? [110, 111] : []), 1, 2, 3, 4]) {
    if (offset >= size - 1) continue
    let value = base + offset
    const octets = Array.from({length: 4}, () => {const byte = value % 256; value = Math.floor(value / 256); return byte}).reverse()
    const ip = octets.join('.')
    if (ip !== context.gateway && ip !== context.server && !ips.includes(ip)) ips.push(ip)
    if (ips.length === 2) break
  }
  return ips
}
export function reservationTemplate(kind: TemplateKind, context: Pick<ReservationContext, 'subnet' | 'gateway' | 'server'>): string[][] {
  const ips = exampleIPs(context)
  return kind === 'mac'
    ? [['规则名称', '固定IP', 'MAC', '启用'], ...ips.map((ip, i) => [`示例-服务器${i + 1}`, ip, `02:00:00:00:00:0${i + 1}`, '是'])]
    : [headers, ...ips.map((ip, i) => [`示例-交换机A-端口${i + 1}`, ip, `port${i + 1}`, '文本', 'switch-a', '文本', '', '是'])]
}
