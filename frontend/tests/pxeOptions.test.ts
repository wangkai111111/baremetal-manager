import assert from 'node:assert/strict'
import test from 'node:test'
import { hostAddressOptions, hostInterfaceOptions, includeSavedOption, retainOrChooseAddress, type HostInterface } from '../src/utils/pxeOptions.ts'

const iface: HostInterface = {name: 'eno2', mac: '', up: true, addresses: [
  {address: '192.0.2.2', prefix: '192.0.2.2/24', subnet: '192.0.2.0/24'},
  {address: '192.0.2.120', prefix: '192.0.2.120/24', subnet: '192.0.2.0/24'},
  {address: '198.51.100.2', prefix: '198.51.100.2/24', subnet: '198.51.100.0/24'}
]}
test('PXE address choices reject subnet mismatches and DHCP pool collisions', () => {
  const options = hostAddressOptions(iface, '192.0.2.0/24', '192.0.2.100', '192.0.2.200')
  assert.deepEqual(options.map(row => row.disabled), [false, true, true])
  assert.equal(retainOrChooseAddress(options, '192.0.2.120'), '192.0.2.2')
  assert.equal(hostAddressOptions(iface, '192.0.2.0/25')[0]?.disabled, true)
})
test('switching NIC clears stale IPv4 unless there is exactly one valid choice', () => {
  assert.equal(retainOrChooseAddress(hostAddressOptions(iface), '192.0.2.2'), '192.0.2.2')
  assert.equal(retainOrChooseAddress(hostAddressOptions(iface), '203.0.113.9'), '')
  assert.equal(retainOrChooseAddress([], '192.0.2.2'), '')
  assert.equal(retainOrChooseAddress(hostAddressOptions({...iface, addresses: iface.addresses.slice(2)}), '192.0.2.2'), '198.51.100.2')
})
test('unusable NICs and missing saved resources are displayed but cannot be selected', () => {
  const options = hostInterfaceOptions([iface, {...iface, name: 'down0', up: false}, {...iface, name: 'empty0', addresses: []}])
  assert.deepEqual(options.map(row => row.disabled), [false, true, true])
  const fallback = includeSavedOption(options, 'removed0')
  assert.equal(fallback.at(-1)?.disabled, true)
  assert.match(fallback.at(-1)!.label, /removed0.*系统未检测到/)
  assert.equal(includeSavedOption(options, 'eno2').length, options.length)
})
