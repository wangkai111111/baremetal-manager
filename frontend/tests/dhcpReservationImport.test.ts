import assert from 'node:assert/strict'
import test from 'node:test'
import * as XLSX from 'xlsx'
import { parseReservationTable, parseReservationText, previewReservationImport, readReservationWorkbook, reservationTemplate } from '../src/utils/dhcpReservationImport.ts'
import { newDHCPReservation } from '../src/utils/dhcpReservations.ts'

const context = {subnet: '10.30.0.0/24', gateway: '10.30.0.1', server: '172.16.20.23', mode: 'relay', taggedOnly: false, relayProxy: false}
const asText = (table: string[][]) => table.map(row => row.join('\t')).join('\r\n')
const existing = () => ({...newDHCPReservation(), name: '已有设备', ip: '10.30.0.110', mac: '02:00:00:00:00:01'})

test('both paste templates produce valid rules, including MAC rules in direct mode and small subnets', () => {
  for (const kind of ['option82', 'mac'] as const) {
    const parsed = parseReservationText('\uFEFF\r\n' + asText(reservationTemplate(kind, context)))
    assert.equal(parsed.error, '')
    assert.deepEqual(parsed.rows.map(row => row.line), [3, 4])
    const preview = previewReservationImport(parsed, [], 'append', {...context, mode: kind === 'mac' ? 'direct' : 'relay'})
    assert.equal(preview.valid, true)
    assert.equal(preview.rules.length, 2)
  }
  const small = {...context, subnet: '10.30.0.0/30'}
  const preview = previewReservationImport(parseReservationTable(reservationTemplate('mac', small)), [], 'replace', small)
  assert.equal(preview.valid, true)
  assert.deepEqual(preview.rules.map(row => row.ip), ['10.30.0.2'])
})

test('CSV quoting, TSV copied from Excel, aliases and exact Relay bytes are retained', () => {
  const csv = 'name,ip,circuit_id,circuit_format,remote_id,remote_format,enabled\n"机柜A,端口""1""",10.30.0.20," port1 ",text,00:04:00:64,hex,0'
  const parsed = parseReservationText(csv)
  assert.equal(parsed.error, '')
  assert.equal(parsed.rows[0]?.rule.name, '机柜A,端口"1"')
  assert.equal(parsed.rows[0]?.rule.circuit_id, ' port1 ')
  assert.equal(parsed.rows[0]?.rule.remote_format, 'hex')
  assert.equal(parsed.rows[0]?.rule.enabled, false)
  assert.equal(previewReservationImport(parsed, [], 'replace', context).valid, true)
  const mac = parseReservationText('规则名称\tIP地址\tMAC地址\t启用\n 机器一 \t 10.30.0.21 \t02-00-00-00-00-02\t')
  assert.equal(mac.rows[0]?.rule.mac, '02:00:00:00:00:02')
  assert.equal(mac.rows[0]?.rule.enabled, true)
})

test('invalid headings, ambiguous delimiters and malformed quotes fail without silently discarding data', () => {
  for (const value of ['', '规则名称,固定IP,MAC', '规则名称,固定IP,MAC,错别字\na,10.30.0.20,02:00:00:00:00:01,x',
    '规则名称,固定IP,IP,MAC\na,10.30.0.20,10.30.0.21,02:00:00:00:00:01', '规则名称,固定IP\na,10.30.0.20',
    '规则名称,固定IP,MAC\n"未闭合,10.30.0.20,02:00:00:00:00:01', '规则名称,固定IP,MAC\n"a"junk,10.30.0.20,02:00:00:00:00:01']) {
    assert.ok(parseReservationText(value).error, value)
  }
  const extra = parseReservationText('规则名称,固定IP,MAC\na,10.30.0.20,02:00:00:00:00:01,不应丢失')
  assert.match(extra.rows[0]!.error, /没有表头/)
  assert.equal(previewReservationImport(extra, [], 'replace', context).valid, false)
})

test('preview reports multiple source lines and rejects the whole batch on invalid enums, IPs or missing matches', () => {
  const table = parseReservationText('规则名称,固定IP,MAC,启用\n\n错误状态,10.30.0.20,02:00:00:00:00:01,随便\n错误IP,10.30.0.1,02:00:00:00:00:02,是\n缺少条件,10.30.0.21,,是\n正常,10.30.0.22,02:00:00:00:00:04,是')
  const preview = previewReservationImport(table, [], 'append', context)
  assert.equal(preview.valid, false)
  assert.deepEqual(preview.rows.filter(row => row.error).map(row => row.line), [3, 4, 5])
  assert.equal(preview.rules.length, 1)
  assert.match(preview.rows[0]!.error, /启用列/)
  assert.match(preview.rows[1]!.error, /网关/)
  assert.match(preview.rows[2]!.error, /至少填写/)
})

test('append checks existing reservations; replace does not mutate originals and needs valid strict policy', () => {
  const old = [existing()]
  const table = parseReservationTable(reservationTemplate('mac', context))
  const append = previewReservationImport(table, old, 'append', context)
  assert.equal(append.valid, false)
  assert.match(append.rows[0]!.error, /固定 IP 已/)
  const replace = previewReservationImport(table, old, 'replace', context)
  assert.equal(replace.valid, true)
  replace.rules[0]!.name = '新对象'
  assert.equal(table.rows[0]!.rule.name, '示例-服务器1')
  assert.equal(old[0]!.name, '已有设备')
  const disabled = parseReservationText('规则名称,固定IP,MAC,启用\na,10.30.0.20,02:00:00:00:00:01,否')
  assert.match(previewReservationImport(disabled, [], 'replace', {...context, taggedOnly: true}).error, /已启用/)
})

test('batch overlap detection compares canonical Option 82 bytes and catches wildcard match collisions', () => {
  const table = parseReservationText('规则名称,固定IP,Circuit-ID,Circuit-ID格式,Remote-ID\na,10.30.0.20,port1,text,switch-a\nb,10.30.0.21,70:6f:72:74:31,hex,switch-a\nc,10.30.0.22,,,switch-a')
  const preview = previewReservationImport(table, [], 'replace', context)
  assert.equal(preview.valid, false)
  assert.deepEqual(preview.rows.filter(row => row.error).map(row => row.line), [3, 4])
  assert.ok(preview.rows[1]!.error.includes('同时命中'))
  const direct = previewReservationImport(table, [], 'replace', {...context, mode: 'direct'})
  assert.ok(direct.rows.every(row => /Relay/.test(row.error)))
})

test('row, column, text and merged rule limits cannot be bypassed', () => {
  const rows = Array.from({length: 129}, (_, i) => [`机器${i}`, `10.30.0.${i + 2}`, `02:00:00:00:01:${i.toString(16).padStart(2, '0')}`])
  const header = ['规则名称', '固定IP', 'MAC']
  assert.match(parseReservationTable([header, ...rows]).error, /最多导入 128/)
  const table = parseReservationTable([header, ...rows.slice(0, 128)])
  assert.match(previewReservationImport(table, [existing()], 'append', context).error, /129/)
  assert.match(parseReservationText('a'.repeat(200001)).error, /过大/)
  assert.match(parseReservationTable([Array.from({length:33}, () => 'x')]).error, /列数/)
})

test('real Excel workbook reads two sheets, blank rows, booleans and hex IDs with original row positions', async () => {
  const workbook = XLSX.utils.book_new()
  const option = reservationTemplate('option82', context)
  option[1]![3] = '十六进制'; option[1]![2] = '00:04:00:64:00:01'
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([[], ...option]), '标签规则')
  XLSX.utils.book_append_sheet(workbook, XLSX.utils.aoa_to_sheet([['规则名称','固定IP','MAC','启用'], ['机器A','10.30.0.20','02:00:00:00:00:01',false]]), 'MAC规则')
  for (const bookType of ['xlsx', 'biff8'] as const) {
    const sheets = await readReservationWorkbook(XLSX.write(workbook, {type:'array', bookType}))
    assert.deepEqual(sheets.map(sheet => sheet.name), ['标签规则', 'MAC规则'])
    assert.deepEqual(sheets[0]!.table.rows.map(row => row.line), [3, 4])
    assert.equal(sheets[0]!.table.rows[0]!.rule.circuit_id, '00:04:00:64:00:01')
    assert.equal(sheets[1]!.table.rows[0]!.rule.enabled, false)
    assert.ok(sheets.every(sheet => previewReservationImport(sheet.table, [], 'replace', context).valid))
  }
})

test('oversized Excel used ranges are rejected rather than silently truncated', async () => {
  const workbook = XLSX.utils.book_new(), sheet = XLSX.utils.aoa_to_sheet(reservationTemplate('mac', context))
  sheet['A1001'] = {v:'不能忽略', t:'s'}; sheet['!ref'] = 'A1:D1001'
  XLSX.utils.book_append_sheet(workbook, sheet, '超限')
  const parsed = await readReservationWorkbook(XLSX.write(workbook, {type:'array', bookType:'xlsx'}))
  assert.match(parsed[0]!.table.error, /1000 行/)
})
