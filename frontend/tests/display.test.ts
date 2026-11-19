import { test } from 'node:test'
import assert from 'node:assert/strict'
import { assetLabel, personLabel } from '../src/utils/display.ts'

test('asset identity uses names, host addresses and serial numbers without database keys', () => {
  assert.equal(assetLabel({id: 987654, name: '计算节点', management_ip: '192.0.2.10/24', bmc_address: '192.0.2.10/32', serial_number: 'CN123'}), '计算节点 · 192.0.2.10 · SN: CN123')
  assert.equal(assetLabel({id: 987654, bmc_address: '2001:db8::1/64'}), '2001:db8::1')
  assert.equal(assetLabel({id: 987654}), '服务器信息不可用')
  assert.equal(assetLabel(null), '服务器信息不可用')
})

test('operator identity preserves login names and avoids duplicate display names', () => {
  assert.equal(personLabel('管理员', 'admin'), '管理员 · admin')
  assert.equal(personLabel('admin', 'admin'), 'admin')
  assert.equal(personLabel(null, 'operator'), 'operator')
  assert.equal(personLabel(null), '操作人信息不可用')
})
